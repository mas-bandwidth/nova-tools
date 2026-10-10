package store

import (
	"context"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Stats reset (`nova-sprint stats reset`, sprint.ResetMarkOf; docs/SPEC-SPRINT.md section 11,
// Statistics): every figure counts from a mark, and nothing moves. One write: the mark, into
// the stats record (keyStats, StatsRecord.Reset) beside the last tidy, never a property of a
// table (a tidy's property per row met the fleet table's property bound on 2026-10-09, 66 of
// 64). No card, stream or archive is touched. The where record is counted again by the next
// tick (keepWhere subtracts the mark's spend) and the work table's cost cells are mirrored
// again from the mark's bases, as after a tidy of the streams.

// ResetReq is a reset: why, and whether it only says what the mark would hold.
type ResetReq struct {
	Reason string
	DryRun bool
}

// ResetResult is what a reset wrote, or with DryRun would write: the mark, and the mark it
// replaced in the epoch (nil with none); Said is what it read and says once (an unreadable
// stats record).
type ResetResult struct {
	Mark     sprint.ResetMark  `json:"mark"`
	Replaced *sprint.ResetMark `json:"replaced,omitempty"`
	DryRun   bool              `json:"dry_run,omitempty"`
	Said     []string          `json:"said,omitempty"`
}

// resetIn is the record's reset mark when it is of the epoch, nil otherwise.
func (rec StatsRecord) resetIn(epoch uint64) *sprint.ResetMark {
	if rec.Reset == nil || rec.Reset.Epoch != epoch {
		return nil
	}
	return rec.Reset
}

// StatsReset is the stats reset's mark in the store's epoch; nil with none, or when the
// store keeps no records.
func (st *Store) StatsReset(ctx context.Context) (*sprint.ResetMark, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	rec, err := st.StatsTidied(ctx)
	if err != nil {
		return nil, err
	}
	return rec.resetIn(st.epoch), nil
}

// ResetStats marks the counters as they stand (sprint.ResetMarkOf over every card of the
// sprint, the tick's own read) and writes the mark into the stats record, replacing the last
// one; the record's tidy (its time, kinds, bases and archives) is kept as it was. Then, as a
// tidy of the streams does, the where record is left to the next tick to count again and the
// display cells are mirrored again. DryRun writes nothing and returns the mark.
func (st *Store) ResetStats(ctx context.Context, req ResetReq) (ResetResult, error) {
	var res ResetResult
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
	s, err := st.Load(ctx, All, tickExtras)
	if err != nil {
		return res, err
	}
	res.Mark = sprint.ResetMarkOf(s, st.now(), st.Actor, req.Reason)
	res.Mark.Epoch = st.epoch
	res.Replaced, res.DryRun = rec.resetIn(st.epoch), req.DryRun
	if req.DryRun {
		return res, nil
	}
	rec.Reset = &res.Mark
	if err := st.putJSON(ctx, keyStats, rec); err != nil {
		return res, err
	}
	// the where record's spend and per landed are counted again by the next tick, from the mark
	if err := kv.SetKey(ctx, keyWhere, ""); err != nil {
		return res, err
	}
	return res, st.SyncMirrors(ctx)
}
