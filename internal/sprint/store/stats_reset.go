package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Stats reset (`nova-sprint stats reset`, sprint.ResetMarkOf; docs/SPEC-SPRINT.md section 11,
// Statistics): every figure counts from a mark, and nothing moves. One write: the mark, into
// the stats record (keyStats, StatsRecord.Reset) beside the last tidy, never a property of a
// table (a tidy's property per row met the fleet table's property bound on 2026-10-09, 66 of
// 64). No card, stream or archive is touched. The where record is counted again by the next
// tick (keepWhere subtracts the mark's spend, and stamps the record with the stats record it
// counted from, statsStamp) and the work table's cost cells are mirrored again from the
// mark's bases, as after a tidy of the streams. The stats record is read, changed and
// written back only by a compare-and-set (updateStats), so a reset and a tidy at once never
// drop each other's writes. tla/StatsReset.tla models it.

// ResetReq is a reset: why, whether it only says what the mark would hold, and the caller's
// operation id (--op), under which a retry returns the mark written and writes nothing.
type ResetReq struct {
	Reason string
	DryRun bool
	Op     string
}

// ResetResult is what a reset wrote, or with DryRun would write: the mark, and the mark it
// replaced in the epoch (nil with none); Replay says the operation id was the mark's already
// and nothing was written; Said is what it read and says once (an unreadable stats record).
type ResetResult struct {
	Mark     sprint.ResetMark  `json:"mark"`
	Replaced *sprint.ResetMark `json:"replaced,omitempty"`
	DryRun   bool              `json:"dry_run,omitempty"`
	Replay   bool              `json:"replay,omitempty"`
	Said     []string          `json:"said,omitempty"`
	// Refused is why the reset wrote nothing (a tidy in flight); "" when it was not refused.
	Refused string `json:"refused,omitempty"`
}

// resetIn is the record's reset mark when it is of the epoch, nil otherwise.
func (rec StatsRecord) resetIn(epoch uint64) *sprint.ResetMark {
	if rec.Reset == nil || rec.Reset.Epoch != epoch {
		return nil
	}
	return rec.Reset
}

// statsStamp is what the where record's spend and per landed are counted from in the epoch:
// the last tidy of the streams and the reset's mark, by their times; "" with neither. The
// tick recounts a where record whose stamp is not the stats record's, and where takes none
// such (keepWhere, WhereFacts): a reset or a tidy between the tick's read of the stats record
// and its write of the where record moves no table, so the revisions alone would keep it.
func (rec StatsRecord) statsStamp(epoch uint64) string {
	var tidied, marked time.Time
	if rec.Epoch == epoch {
		tidied = rec.Kinds[sprint.TidyStreams]
	}
	if m := rec.resetIn(epoch); m != nil {
		marked = m.At
	}
	if tidied.IsZero() && marked.IsZero() {
		return ""
	}
	return tidied.UTC().Format(time.RFC3339Nano) + "|" + marked.UTC().Format(time.RFC3339Nano)
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
// one; the record's tidy (its time, kinds, bases and archives) is kept as it is at the write
// (updateStats). Then, as a tidy of the streams does, the where record is left to the next
// tick to count again and the display cells are mirrored again. DryRun writes nothing and
// returns the mark. With an operation id, the same id again returns the mark it wrote and
// writes nothing; an id recorded for another verb, or for a reset with another reason, is
// refused (OpConflictError), as a step's is.
func (st *Store) ResetStats(ctx context.Context, req ResetReq) (ResetResult, error) {
	var res ResetResult
	const verb = "stats reset"
	if req.Op != "" && (strings.Contains(req.Op, "~") || !callerOpWord(req.Op)) {
		return res, fmt.Errorf("operation id %s is not one word of letters, digits, '_', '-' and '.' with no '~': nothing was done; give the reset an --op of that shape", req.Op)
	}
	st, err := st.pin(ctx)
	if err != nil {
		return res, err
	}
	kv, err := st.kv()
	if err != nil {
		return res, err
	}
	if req.Op != "" {
		// an id another verb's step recorded is not this reset's (engine.go, callerOp)
		r, done, err := st.callerOp(ctx, Step{Verb: verb, CallerOp: req.Op}, Result{Verb: verb})
		if err != nil {
			return res, err
		}
		if done {
			if len(r.Refused) > 0 {
				return res, errors.New(r.Refused[0].Why)
			}
			return res, &OpConflictError{Op: req.Op, Verb: verb, Recorded: r.Verb}
		}
	}
	// the mark is counted inside the compare-and-set: each try reads the stats record, then
	// the sprint, and writes only while the record is what it read, so a tidy that began or
	// ended between (its marker, its kinds, its archive) has the mark counted again from the
	// fleet rows as they are after it; a tidy in flight refuses the reset
	sw, swaps := kv.(keySwapper)
	wrote := false
	for try := 0; try < statsSwapTries && !wrote; try++ {
		raw, had, err := kv.GetKey(ctx, keyStats)
		if err != nil {
			return res, err
		}
		rec := st.statsRecordOf(raw, had)
		res.Said = append(res.Said, st.stats().takeNotes()...)
		if t := rec.Tidying; t.Fresh(st.now()) {
			res.Refused = fmt.Sprintf("a stats tidy is in flight (archive %s, since %s): a mark counted now could hold cards it is taking off; nothing written; run it again after it ends", t.Archive, t.At.Format(time.RFC3339Nano))
			return res, nil
		}
		if m := rec.resetIn(st.epoch); m != nil && req.Op != "" && m.Op == req.Op {
			if m.Reason != req.Reason {
				return res, &OpConflictError{Op: req.Op, Verb: verb, Recorded: verb, OtherArgs: true}
			}
			res.Mark, res.Replay = *m, true
			return res, nil
		}
		s, err := st.Load(ctx, All, tickExtras)
		if err != nil {
			return res, err
		}
		res.Mark = sprint.ResetMarkOf(s, st.now(), st.Actor, req.Reason)
		res.Mark.Epoch, res.Mark.Op = st.epoch, req.Op
		res.Replaced, res.DryRun = rec.resetIn(st.epoch), req.DryRun
		if req.DryRun {
			return res, nil
		}
		mark := res.Mark
		rec.Reset = &mark
		b, err := json.Marshal(rec)
		if err != nil {
			return res, err
		}
		if !swaps {
			if err := kv.SetKey(ctx, keyStats, string(b)); err != nil {
				return res, err
			}
			break
		}
		if wrote, err = sw.SwapKey(ctx, keyStats, raw, had, string(b)); err != nil {
			return res, err
		}
	}
	if swaps && !wrote {
		return res, fmt.Errorf("the stats record %q kept changing under the reset (%d tries): nothing written; run it again", keyStats, statsSwapTries)
	}
	// the where record's spend and per landed are counted again by the next tick, from the mark
	if err := kv.SetKey(ctx, keyWhere, ""); err != nil {
		return res, err
	}
	return res, st.SyncMirrors(ctx)
}

// keySwapper is a store that writes a record only while it holds what was read: Redis
// (WATCH, then MULTI/EXEC) and the twin (under its lock).
type keySwapper interface {
	SwapKey(ctx context.Context, name, was string, had bool, value string) (bool, error)
}

// statsSwapTries bounds updateStats' read, change and compare-and-set.
const statsSwapTries = 16

// updateStats reads the stats record, changes it and writes it back only while it is still
// what was read, read again and changed again when another writer (a reset, a tidy) wrote
// between; a store with no compare-and-set writes it plainly. A change that returns an error
// writes nothing and is that error. It returns the record written.
func (st *Store) updateStats(ctx context.Context, kv KV, change func(rec *StatsRecord) error) (StatsRecord, error) {
	for range statsSwapTries {
		raw, had, err := kv.GetKey(ctx, keyStats)
		if err != nil {
			return StatsRecord{}, err
		}
		rec := st.statsRecordOf(raw, had)
		if err := change(&rec); err != nil {
			return rec, err
		}
		b, err := json.Marshal(rec)
		if err != nil {
			return rec, err
		}
		sw, ok := kv.(keySwapper)
		if !ok {
			return rec, kv.SetKey(ctx, keyStats, string(b))
		}
		done, err := sw.SwapKey(ctx, keyStats, raw, had, string(b))
		if err != nil || done {
			return rec, err
		}
	}
	return StatsRecord{}, fmt.Errorf("the stats record %q kept changing under the write (%d tries): nothing written; run it again", keyStats, statsSwapTries)
}

// SwapKey writes the record only while it holds was (absent when had is false), under the
// twin's lock: false, nothing written, when it holds anything else.
func (m *Mem) SwapKey(_ context.Context, name, was string, had bool, value string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count("kv")
	if err := m.fail("kv"); err != nil {
		return false, err
	}
	cur, ok := m.kv[name]
	if ok != had || cur != was {
		return false, nil
	}
	if m.kv == nil {
		m.kv = map[string]string{}
	}
	m.kv[name] = value
	return true, nil
}

// errKeyMoved is a record another writer changed under a SwapKey.
var errKeyMoved = errors.New("the record moved")

// SwapKey writes the record only while it holds was (absent when had is false): WATCH on
// it, its value read, then MULTI/EXEC; false, nothing written, when it moved.
func (r *Redis) SwapKey(ctx context.Context, name, was string, had bool, value string) (bool, error) {
	key := r.Names.Key(name)
	err := r.C.Watch(ctx, func(tx *redis.Tx) error {
		cur, err := tx.Get(ctx, key).Result()
		switch {
		case errors.Is(err, redis.Nil):
			if had {
				return errKeyMoved
			}
		case err != nil:
			return err
		case !had || cur != was:
			return errKeyMoved
		}
		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.Set(ctx, key, value, 0)
			return nil
		})
		return err
	}, key)
	if errors.Is(err, errKeyMoved) || errors.Is(err, redis.TxFailedErr) {
		return false, nil
	}
	return err == nil, err
}
