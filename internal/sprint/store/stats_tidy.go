package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

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
	// Reset is the last stats reset (ResetStats, stats_reset.go): the counters at its mark,
	// which every figure it covers counts from. A tidy keeps it, rebased by the cards it took
	// off that finished before it (sprint.ResetMark.Rebase).
	Reset *sprint.ResetMark `json:"reset,omitempty"`
	// Tidying is a tidy in flight: written before it moves anything, cleared by its last
	// write. A reset is refused while it stands (TidyInFlight.Fresh), so a mark is never
	// counted from a sprint a tidy is moving, and the tidy rebases only the mark it saw here.
	Tidying *TidyInFlight `json:"tidying,omitempty"`
}

// TidyInFlight is a tidy that began and has not written its end: its archive, when it began,
// and the reset's mark in force then (its time; zero for none).
type TidyInFlight struct {
	Archive string    `json:"archive"`
	At      time.Time `json:"at"`
	Mark    time.Time `json:"mark,omitzero"`
}

// TidyStale is how long a tidy in flight stands: one whose process died leaves its marker,
// and past this a reset or another tidy goes on, saying so.
const TidyStale = 10 * time.Minute

// Fresh is whether the tidy in flight still stands at now.
func (t *TidyInFlight) Fresh(now time.Time) bool {
	return t != nil && now.Sub(t.At) < TidyStale
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
//
// A stats reset (ResetStats) later than that tidy is where they start instead: the reset's
// mark covers every kind.
func (st *Store) StatsSince(ctx context.Context, kind string) (time.Time, error) {
	rec, err := st.StatsTidied(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return rec.sinceIn(st.epoch, kind), nil
}

// sinceIn is when the statistics of the kind start in the epoch: its last tidy ("" any
// kind's), or the reset when that is later; zero with neither.
func (rec StatsRecord) sinceIn(epoch uint64, kind string) time.Time {
	var t time.Time
	if rec.Epoch == epoch {
		t = rec.Since
		if kind != "" {
			t = rec.Kinds[kind]
		}
	}
	if m := rec.resetIn(epoch); m.Later(t) {
		t = m.At
	}
	return t
}

// streamBases is the streams' bases the work table's cost column counts from: the
// record's in the store's epoch, none otherwise.
//
// A stats reset later than the last tidy of the streams gives the bases instead (each
// stream's at its mark, sprint.ResetStream.Base): the later of the two is where the cost
// column counts from.
func (st *Store) streamBases(ctx context.Context) (map[string]sprint.StreamBase, error) {
	rec, err := st.StatsTidied(ctx)
	if err != nil {
		return nil, err
	}
	return rec.basesIn(st.epoch), nil
}

// basesIn is the streams' bases in the epoch: the last tidy's of the streams, or the
// reset's when it is later; none with neither.
func (rec StatsRecord) basesIn(epoch uint64) map[string]sprint.StreamBase {
	var bases map[string]sprint.StreamBase
	var tidied time.Time
	if rec.Epoch == epoch {
		bases, tidied = rec.Streams, rec.Kinds[sprint.TidyStreams]
	}
	if m := rec.resetIn(epoch); m.Later(tidied) {
		bases = make(map[string]sprint.StreamBase, len(m.Streams))
		for stream, b := range m.Streams {
			bases[stream] = b.Base()
		}
	}
	return bases
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
// tidied row's median run wall is carried, sprint.PropCarriedMedian); then the archive is
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
	// the tidy in flight, before anything is written or moves: a reset is refused while it
	// stands, and the mark in force now is the one this tidy rebases at its end
	var inFlight TidyInFlight
	if _, err := st.updateStats(ctx, kv, func(rec *StatsRecord) error {
		if t := rec.Tidying; t.Fresh(st.now()) {
			return fmt.Errorf("another stats tidy is in flight (archive %s, since %s): nothing written; run it again after it ends", t.Archive, t.At.Format(time.RFC3339Nano))
		} else if t != nil {
			res.Said = append(res.Said, fmt.Sprintf("the stats tidy in flight since %s (archive %s) is past %s: taken as ended", t.At.Format(time.RFC3339Nano), t.Archive, TidyStale))
		}
		inFlight = TidyInFlight{Archive: res.Archive, At: now}
		if m := rec.resetIn(st.epoch); m != nil {
			inFlight.Mark = m.At
		}
		marker := inFlight
		rec.Tidying = &marker
		return nil
	}); err != nil {
		return res, err
	}
	// the archive first: whatever the move does, what it was to move is kept
	if err := st.putJSON(ctx, res.Archive, res.Record); err != nil {
		return res, err
	}
	keepArchive := func(rec *StatsRecord) {
		if !slices.Contains(rec.Archives, res.Archive) {
			rec.Archives = append(rec.Archives, res.Archive)
		}
		if rec.Tidying != nil && rec.Tidying.Archive == res.Archive {
			rec.Tidying = nil // this tidy's end
		}
	}
	if has(sprint.TidyFriends) || has(sprint.TidyFleet) {
		var rows []sprint.TidyRow
		r, err := st.Run(ctx, Step{Verb: "stats tidy", Load: []string{sprint.Work, sprint.Fleet}, Plan: func(s *sprint.Snapshot) sprint.Plan {
			var p sprint.Plan
			rows, p = sprint.TidyDone(s, kinds, m.StoppedBetween)
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
			_ = st.putJSON(ctx, res.Archive, res.Record)                                                  // ignored: the move's error is the one to report; the planned archive stands
			_, _ = st.updateStats(ctx, kv, func(rec *StatsRecord) error { keepArchive(rec); return nil }) // ignored: as above; the record keeps the archive's key for teardown
			return res, fmt.Errorf("%w; the archive %s is kept (state failed, or planned when that write failed too); run it again after a minute", err, res.Archive)
		}
		res.Record.Rows = rows
		res.count()
	}
	res.Record.State = ArchiveDone
	if err := st.putJSON(ctx, res.Archive, res.Record); err != nil {
		return res, err
	}
	// the record, read again and written only while unchanged (updateStats): a reset written
	// since this tidy's first read is kept, its rows rebased on what this tidy took off
	if _, err := st.updateStats(ctx, kv, func(rec *StatsRecord) error {
		keepArchive(rec)
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
		// a reset's rows count from the mark: the cards this tidy took off that finished
		// before the mark leave its counters too (sprint.ResetMark.Rebase), so the row's done
		// since the mark is the cards since it still on the row. Only the mark this tidy saw
		// when it began is rebased: that one was counted before the move; a mark written since
		// (past a stale marker) was counted after it and holds none of the cards moved
		if m := rec.resetIn(st.epoch); m != nil && m.At.Equal(inFlight.Mark) {
			rebased := *m
			rebased.Rows = maps.Clone(m.Rows)
			rebased.Rebase(res.Record.Rows)
			rec.Reset = &rebased
		}
		return nil
	}); err != nil {
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
