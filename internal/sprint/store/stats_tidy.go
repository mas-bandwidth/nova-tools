package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Stats tidy (`nova-sprint stats tidy`, sprint.TidyDone; docs/SPEC-SPRINT.md section 11,
// Statistics): the statistics start afresh and the work is kept. Two kinds of record
// under the deployment's prefix, outside the tables, one for the whole sprint as the
// machine's are: stats, the last tidy (when, why, by whom, the kinds it named and each
// kind's time, the streams' bases and every archive's key), and
// stats:archive:<RFC3339Nano>-<nonce>, one for each tidy, written before anything moves:
// each fleet row's ok and failed counts and the cards it took off (with the cell each came
// from) and left, the route counters, the streams' bases, and how the move ended.

// keyStats is the stats record (StatsRecord).
const keyStats = "stats"

// StatsArchiveKey is the archive record of the tidy at at, to the nanosecond, UTC, with a
// nonce: two tidies never share a key.
func StatsArchiveKey(at time.Time, nonce string) string {
	return "stats:archive:" + at.UTC().Format(time.RFC3339Nano) + "-" + nonce
}

// StatsRecord is the last tidy as stored.
type StatsRecord struct {
	Epoch  uint64    `json:"epoch"`
	Since  time.Time `json:"since,omitzero"` // the last tidy, of any kind, to the nanosecond
	Reason string    `json:"reason,omitempty"`
	By     string    `json:"by,omitempty"`
	// Kinds is each kind's last tidy (sprint.TidyKinds).
	Kinds map[string]time.Time `json:"kinds,omitempty"`
	// Streams is each stream's landed cost and count at the last tidy of the streams: the
	// work table's cost cell and per landed count from them (sprint.StreamCostSince).
	Streams map[string]sprint.StreamBase `json:"streams,omitempty"`
	// Archives is every archive record's key, oldest first: teardown deletes each by name.
	Archives []string `json:"archives,omitempty"`
}

// The states of an archive record: written before the move, then how the move ended.
const (
	ArchivePlanned = "planned"
	ArchiveDone    = "done"
	ArchiveFailed  = "failed"
)

// StatsArchive is one tidy's archive record: the counters it moved.
type StatsArchive struct {
	At     time.Time `json:"at"`
	Epoch  uint64    `json:"epoch"`
	Reason string    `json:"reason"`
	By     string    `json:"by,omitempty"`
	Kinds  []string  `json:"kinds"`
	// State is ArchivePlanned until the move ends, then ArchiveDone or ArchiveFailed
	// (Error says why). Rows are the plan's while planned and what moved once done.
	State   string                       `json:"state"`
	Error   string                       `json:"error,omitempty"`
	Rows    []sprint.TidyRow             `json:"rows,omitempty"`
	Routes  []sprint.RouteStat           `json:"routes,omitempty"`
	Streams map[string]sprint.StreamBase `json:"streams,omitempty"`
}

// TidyReq is a tidy: the kinds it names, why, and whether it only says what would move.
type TidyReq struct {
	Kinds  []string
	Reason string
	DryRun bool
}

// TidyResult is what a tidy did, or with DryRun would do; Refused is why it did
// nothing, "" when it was not refused; Said is what it read and says once (an
// unreadable stats record).
type TidyResult struct {
	Archive string       `json:"archive"`
	DryRun  bool         `json:"dry_run,omitempty"`
	Refused string       `json:"refused,omitempty"`
	Said    []string     `json:"said,omitempty"`
	Record  StatsArchive `json:"record"`
	Moved   int          `json:"moved"`
	Kept    int          `json:"kept"`
}

// statsRecordOf is the stats record from its stored value. Permissive: an unreadable one
// is no tidy recorded, said once (the tick's notes), and never fails a tick, a mirror sync
// or a read.
func (st *Store) statsRecordOf(raw string, ok bool) StatsRecord {
	var rec StatsRecord
	if !ok || raw == "" {
		return rec
	}
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		st.stats().note(fmt.Sprintf("the stats record %q is unreadable (%v): counted as no tidy recorded; the next stats tidy writes it again", keyStats, err))
		return StatsRecord{}
	}
	return rec
}

// StatsTidied is the stats record; the zero record when there is none, it does not read
// (statsRecordOf), or the store keeps no records.
func (st *Store) StatsTidied(ctx context.Context) (StatsRecord, error) {
	kv, err := st.kv()
	if err != nil {
		return StatsRecord{}, nil
	}
	raw, ok, err := kv.GetKey(ctx, keyStats)
	if err != nil {
		return StatsRecord{}, err
	}
	return st.statsRecordOf(raw, ok), nil
}

// StatsSince is when the statistics of the kind start: its last tidy in the store's
// epoch, zero when there was none ("" is any kind's, the last tidy).
func (st *Store) StatsSince(ctx context.Context, kind string) (time.Time, error) {
	rec, err := st.StatsTidied(ctx)
	if err != nil || rec.Epoch != st.epoch {
		return time.Time{}, err
	}
	if kind == "" {
		return rec.Since, nil
	}
	return rec.Kinds[kind], nil
}

// streamBases is the streams' bases the work table's cost column counts from: the
// record's in the store's epoch, none otherwise.
func (st *Store) streamBases(ctx context.Context) (map[string]sprint.StreamBase, error) {
	rec, err := st.StatsTidied(ctx)
	if err != nil || rec.Epoch != st.epoch {
		return nil, err
	}
	return rec.Streams, nil
}

// archiveNonce is a short random nonce for an archive's key.
func archiveNonce() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b) // ignored: crypto/rand.Read never fails on the platforms we build for
	return hex.EncodeToString(b)
}

// TidyStats starts the statistics of the kinds afresh and keeps the work. The archive
// record (StatsArchiveKey) is written first, planned, with the route counters (named by
// --routes, and by --fleet or --friends, whose cards they count), the streams' bases and
// the rows' plan; then the history-only finished cards leave the done cells of the
// friends' or the machines' rows in one step (sprint.TidyDone; their records stay; each
// tidied row's median run wall is carried, sprint.PropCarriedMedians); then the archive is
// written again with what moved and how the move ended, and the stats record names the
// tidy. A move that fails leaves its archive, failed. Cards on any other cell, the work,
// merge and readers tables, holds and judgments are untouched. A tidy within
// sprint.TidyAgainAfter of the last, to the nanosecond, is refused, nothing written;
// DryRun writes nothing and says what would move.
func (st *Store) TidyStats(ctx context.Context, req TidyReq) (TidyResult, error) {
	var res TidyResult
	st, err := st.pin(ctx)
	if err != nil {
		return res, err
	}
	kv, err := st.kv()
	if err != nil {
		return res, err
	}
	raw, had, err := kv.GetKey(ctx, keyStats)
	if err != nil {
		return res, err
	}
	rec := st.statsRecordOf(raw, had)
	res.Said = st.stats().takeNotes()
	now := st.now().UTC()
	if !rec.Since.IsZero() && now.Sub(rec.Since) < sprint.TidyAgainAfter && !now.Before(rec.Since) {
		res.Refused = fmt.Sprintf("the last tidy was at %s, %s ago; a second within %s is refused, nothing written",
			rec.Since.Format(time.RFC3339Nano), now.Sub(rec.Since), sprint.TidyAgainAfter)
		return res, nil
	}
	m, _, err := st.Machine(ctx)
	if err != nil {
		return res, err
	}
	kinds := slices.Clone(req.Kinds)
	has := func(k string) bool { return slices.Contains(kinds, k) }
	res.Archive, res.DryRun = StatsArchiveKey(now, archiveNonce()), req.DryRun
	res.Record = StatsArchive{At: now, Epoch: st.epoch, Reason: req.Reason, By: st.Actor, Kinds: kinds, State: ArchivePlanned}
	s, err := st.Load(ctx, All, nil)
	if err != nil {
		return res, err
	}
	if has(sprint.TidyRoutes) || has(sprint.TidyFleet) || has(sprint.TidyFriends) {
		routes, _, err := st.Routes(ctx)
		if err != nil {
			return res, err
		}
		res.Record.Routes = sprint.RouteStats(routes, s.Fleet)
	}
	if has(sprint.TidyStreams) {
		res.Record.Streams = sprint.StreamBases(s)
	}
	res.Record.Rows, _ = sprint.TidyDone(s, kinds, m.StoppedBetween)
	res.count()
	if req.DryRun {
		return res, nil
	}
	// the archive first: whatever the move does, what it was to move is kept
	if err := st.putJSON(ctx, res.Archive, res.Record); err != nil {
		return res, err
	}
	rec.Archives = append(rec.Archives, res.Archive)
	if has(sprint.TidyFriends) || has(sprint.TidyFleet) {
		var rows []sprint.TidyRow
		r, err := st.Run(ctx, Step{Verb: "stats tidy", Load: []string{sprint.Work, sprint.Fleet}, Plan: func(s *sprint.Snapshot) sprint.Plan {
			var p sprint.Plan
			rows, p = sprint.TidyDone(s, kinds, m.StoppedBetween)
			if len(p.Props) > 0 {
				st.dropOldCarriedMedians(ctx)
			}
			return p
		}})
		switch {
		case err != nil:
		case r.Lost:
			err = fmt.Errorf("the done cells kept changing under it (%d tries); nothing moved", r.Attempts)
		case len(r.Refused) > 0:
			err = fmt.Errorf("%d of its moves were refused (%s)", len(r.Refused), r.Refused[0].Why)
		}
		if err != nil {
			res.Record.State, res.Record.Error = ArchiveFailed, err.Error()
			_ = st.putJSON(ctx, res.Archive, res.Record) // ignored: the move's error is the one to report; the planned archive stands
			_ = st.putJSON(ctx, keyStats, rec)           // ignored: as above; the record keeps the archive's key for teardown
			return res, fmt.Errorf("%w; the archive %s is kept (state failed, or planned when that write failed too); run it again after a minute", err, res.Archive)
		}
		res.Record.Rows = rows
		res.count()
	}
	res.Record.State = ArchiveDone
	if err := st.putJSON(ctx, res.Archive, res.Record); err != nil {
		return res, err
	}
	if rec.Epoch != st.epoch {
		rec.Kinds, rec.Streams = nil, nil
	}
	rec.Epoch, rec.Since, rec.Reason, rec.By = st.epoch, now, req.Reason, st.Actor
	if rec.Kinds == nil {
		rec.Kinds = map[string]time.Time{}
	}
	for _, k := range kinds {
		rec.Kinds[k] = now
	}
	if has(sprint.TidyStreams) {
		rec.Streams = res.Record.Streams
	}
	if err := st.putJSON(ctx, keyStats, rec); err != nil {
		return res, err
	}
	if has(sprint.TidyStreams) {
		// the where record's per landed is counted again by the next tick, from the bases
		if err := kv.SetKey(ctx, keyWhere, ""); err != nil {
			return res, err
		}
		if err := st.SyncMirrors(ctx); err != nil {
			return res, err
		}
	}
	return res, nil
}

// count is the result's moved and kept, from its rows.
func (res *TidyResult) count() {
	res.Moved, res.Kept = 0, 0
	for _, r := range res.Record.Rows {
		res.Moved += len(r.Moved)
		res.Kept += len(r.Kept)
	}
}

// dropOldCarriedMedians drops legacy per-row carried_median_<row> properties from the
// fleet table so a live fleet table near the bound shrinks rather than grows (docs/SPEC-SPRINT.md
// section 11).
func (st *Store) dropOldCarriedMedians(ctx context.Context) {
	fleetTable := st.Names.Table(sprint.Fleet)
	b := st.B
	if b == nil {
		b = st.root
	}
	m := unwrapMem(b)
	if m == nil && st.root != nil {
		m = unwrapMem(st.root)
	}
	if m != nil {
		m.mu.Lock()
		if t := m.tables[fleetTable]; t != nil {
			ep := t.at(st.epoch)
			if ep != nil && ep.props != nil {
				for k := range ep.props {
					if strings.HasPrefix(k, "carried_median_") {
						delete(ep.props, k)
					}
				}
			}
		}
		m.mu.Unlock()
	} else {
		r := unwrapRedis(b)
		if r == nil && st.root != nil {
			r = unwrapRedis(st.root)
		}
		if r != nil {
			propsKey := ntable.PropsKeyAt(fleetTable, st.epoch)
			keys, err := r.C.HKeys(ctx, propsKey).Result()
			if err == nil {
				var toDel []string
				for _, k := range keys {
					if strings.HasPrefix(k, "carried_median_") {
						toDel = append(toDel, k)
					}
				}
				if len(toDel) > 0 {
					_ = r.C.HDel(ctx, propsKey, toDel...).Err() // ignored: dropping old properties so the table shrinks under LimitTableProps
				}
			}
		}
	}
	if st.tw != nil {
		st.tw.mu.Lock()
		if fl, ok := st.tw.tables[sprint.Fleet]; ok && fl != nil {
			p := fl.Props()
			changed := false
			for k := range p {
				if strings.HasPrefix(k, "carried_median_") {
					delete(p, k)
					changed = true
				}
			}
			if changed {
				fl.SetProps(p)
			}
		}
		st.tw.mu.Unlock()
	}
}

func unwrapMem(b Backend) *Mem {
	for b != nil {
		if m, ok := b.(*Mem); ok {
			return m
		}
		if u, ok := b.(interface{ Unwrap() Backend }); ok {
			b = u.Unwrap()
		} else if w, ok := b.(interface{ RawBackend() Backend }); ok {
			b = w.RawBackend()
		} else {
			break
		}
	}
	return nil
}

func unwrapRedis(b Backend) *Redis {
	for b != nil {
		if r, ok := b.(*Redis); ok {
			return r
		}
		if u, ok := b.(interface{ Unwrap() Backend }); ok {
			b = u.Unwrap()
		} else if w, ok := b.(interface{ RawBackend() Backend }); ok {
			b = w.RawBackend()
		} else {
			break
		}
	}
	return nil
}

