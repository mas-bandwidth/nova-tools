package store

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Stats tidy (`nova-sprint stats tidy`, sprint.TidyDone; docs/SPEC-SPRINT.md,
// Statistics): the statistics start afresh and the work is kept. Two kinds of record
// under the deployment's prefix, outside the tables, one for the whole sprint as the
// machine's are: stats, the last tidy (when, why, by whom, the kinds it named and
// each kind's time, and the streams' bases), and stats:archive:<RFC3339>, one for each
// tidy, what it moved: each fleet row's ok and failed counts and the cards it took off
// and left, the route counters, and the streams' bases.

// keyStats is the stats record (StatsRecord).
const keyStats = "stats"

// StatsArchiveKey is the archive record of the tidy at at, to the second, UTC.
func StatsArchiveKey(at time.Time) string {
	return "stats:archive:" + at.UTC().Format(time.RFC3339)
}

// StatsRecord is the last tidy as stored.
type StatsRecord struct {
	Epoch  uint64    `json:"epoch"`
	Since  time.Time `json:"since,omitzero"` // the last tidy, of any kind
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

// StatsArchive is one tidy's archive record: the counters it moved.
type StatsArchive struct {
	At      time.Time                    `json:"at"`
	Epoch   uint64                       `json:"epoch"`
	Reason  string                       `json:"reason"`
	By      string                       `json:"by,omitempty"`
	Kinds   []string                     `json:"kinds"`
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
// nothing, "" when it was not refused.
type TidyResult struct {
	Archive string       `json:"archive"`
	DryRun  bool         `json:"dry_run,omitempty"`
	Refused string       `json:"refused,omitempty"`
	Record  StatsArchive `json:"record"`
	Moved   int          `json:"moved"`
	Kept    int          `json:"kept"`
}

// StatsTidied is the stats record; the zero record when there is none or the store keeps no
// records.
func (st *Store) StatsTidied(ctx context.Context) (StatsRecord, error) {
	var rec StatsRecord
	if _, err := st.kv(); err != nil {
		return rec, nil
	}
	return rec, st.getJSON(ctx, keyStats, &rec)
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

// TidyStats starts the statistics of the kinds afresh and keeps the work: the history-only
// finished cards leave the done cells of the friends' or the machines' rows
// (sprint.TidyDone; their records stay), the route counters and the streams' bases are
// taken, everything moved is written to a dated archive record (StatsArchiveKey), and the
// stats record names the tidy. Cards on any other cell, the work, merge and readers
// tables, holds and judgments are untouched. A tidy within sprint.TidyAgainAfter of the
// last is refused, nothing written; DryRun writes nothing and says what would move.
func (st *Store) TidyStats(ctx context.Context, req TidyReq) (TidyResult, error) {
	var res TidyResult
	st, err := st.pin(ctx)
	if err != nil {
		return res, err
	}
	if _, err := st.kv(); err != nil {
		return res, err
	}
	rec, err := st.StatsTidied(ctx)
	if err != nil {
		return res, err
	}
	now := st.now()
	if !rec.Since.IsZero() && now.Sub(rec.Since) < sprint.TidyAgainAfter && now.Sub(rec.Since) >= 0 {
		res.Refused = fmt.Sprintf("the last tidy was at %s, %s ago (%s); a second within %s is refused, nothing written",
			rec.Since.UTC().Format(time.RFC3339), now.Sub(rec.Since).Round(time.Second), StatsArchiveKey(rec.Since), sprint.TidyAgainAfter)
		return res, nil
	}
	kinds := slices.Clone(req.Kinds)
	has := func(k string) bool { return slices.Contains(kinds, k) }
	res.Archive, res.DryRun = StatsArchiveKey(now), req.DryRun
	res.Record = StatsArchive{At: now.UTC().Truncate(time.Second), Epoch: st.epoch, Reason: req.Reason, By: st.Actor, Kinds: kinds}
	s, err := st.Load(ctx, All, nil)
	if err != nil {
		return res, err
	}
	if has(sprint.TidyRoutes) {
		routes, _, err := st.Routes(ctx)
		if err != nil {
			return res, err
		}
		res.Record.Routes = sprint.RouteStats(routes, s.Fleet)
	}
	if has(sprint.TidyStreams) {
		res.Record.Streams = sprint.StreamBases(s)
	}
	rows, _ := sprint.TidyDone(s, kinds)
	if !req.DryRun && (has(sprint.TidyFriends) || has(sprint.TidyFleet)) {
		r, err := st.Run(ctx, Step{Verb: "stats tidy", Load: []string{sprint.Work, sprint.Fleet}, Plan: func(s *sprint.Snapshot) sprint.Plan {
			var p sprint.Plan
			rows, p = sprint.TidyDone(s, kinds)
			return p
		}})
		if err != nil {
			return res, err
		}
		if r.Lost || len(r.Refused) > 0 {
			return res, fmt.Errorf("the done cells kept changing under it (%d refused); the cards it moved are off the table with their records kept, the archive is not written; run it again after a minute", len(r.Refused))
		}
	}
	res.Record.Rows = rows
	for _, r := range rows {
		res.Moved += len(r.Moved)
		res.Kept += len(r.Kept)
	}
	if req.DryRun {
		return res, nil
	}
	if err := st.putJSON(ctx, res.Archive, res.Record); err != nil {
		return res, err
	}
	if rec.Epoch != st.epoch {
		rec.Kinds, rec.Streams = nil, nil
	}
	rec.Epoch, rec.Since, rec.Reason, rec.By = st.epoch, res.Record.At, req.Reason, st.Actor
	if rec.Kinds == nil {
		rec.Kinds = map[string]time.Time{}
	}
	for _, k := range kinds {
		rec.Kinds[k] = res.Record.At
	}
	if has(sprint.TidyStreams) {
		rec.Streams = res.Record.Streams
	}
	rec.Archives = append(rec.Archives, res.Archive)
	if err := st.putJSON(ctx, keyStats, rec); err != nil {
		return res, err
	}
	if has(sprint.TidyStreams) {
		// the where record's per landed is counted again by the next tick, from the bases
		if kv, err := st.kv(); err == nil {
			if err := kv.SetKey(ctx, keyWhere, ""); err != nil {
				return res, err
			}
		}
		if err := st.SyncMirrors(ctx); err != nil {
			return res, err
		}
	}
	return res, nil
}
