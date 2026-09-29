package store

import (
	"context"
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// ClearResult is what clear did.
type ClearResult struct {
	From, To uint64
	At       time.Time
	// Held is what the old epoch held: primaries, work cards, read cards and
	// merge cards on the tables, and open judgments.
	Held      map[string]int
	Finished  string // a pending operation of the old epoch clear finished first
	Abandoned string // one it could not finish, abandoned with the old epoch
	Restored  bool   // a clear cut before it restored its shape, finished first
	Machine   string // the machine's state before clear set it STOPPED
}

// Clear stops the sprint and clears all work in it: it sets the machine
// STOPPED, as its first act, and leaves it STOPPED; then advances the
// sprint's epoch, once, atomically, recording only that the new epoch owes the
// restore of the old one's shape. Nothing is deleted: the old epoch stays
// where it is and readable (At), and every writer still holding it is refused
// by the table layer as stale, so from the advance on the old epoch is frozen.
// The shape (streams, readers, members and their status) is read after the
// advance, from the frozen old epoch, and restored at the new one: its rows
// and control cards; the notifications, judgments, cursor and fence of the
// new epoch are its own, empty. A pending operation of the old epoch is
// finished first, or abandoned with the old epoch. A restore still owed (a
// clear cut between its advance and its restore) is performed first, by the
// next verb, tick or clear.
func (st *Store) Clear(ctx context.Context) (ClearResult, error) {
	var res ClearResult
	es, err := st.EpochNow(ctx)
	if err != nil {
		return res, err
	}
	// The machine first: no tick begins after it is STOPPED, and a tick in
	// flight is refused as stale at its next part. Clear leaves it STOPPED.
	if _, ok := st.B.(KV); ok {
		before, _, _, err := st.SetMachine(ctx, false)
		if err != nil {
			return res, fmt.Errorf("stopping the machine: %w", err)
		}
		res.Machine = before.StateWord()
		// The people and their goals are the sprint's and are kept; their
		// push times start again with the new sprint.
		if err := st.ResetGoalPushes(ctx); err != nil {
			return res, fmt.Errorf("resetting the goals' pushes: %w", err)
		}
	}
	st, err = st.repin(ctx)
	if err != nil {
		return res, err
	}
	res.Restored = es.Owed
	f, err := st.B.ReadFence(ctx)
	if err != nil {
		return res, err
	}
	if f.Pending != nil {
		r, err := st.finish(ctx, *f.Pending)
		if err != nil {
			return res, err
		}
		if r.Done == RepairOpen {
			if err := st.B.Release(ctx, abandonment(*f.Pending, st.Actor, st.now()), true); err != nil {
				return res, err
			}
			res.Abandoned = f.Pending.ID
		} else {
			res.Finished = f.Pending.ID
		}
	}
	res.From, res.To, res.At = st.epoch, st.epoch+1, st.now()
	ok, err := st.root.AdvanceEpoch(ctx, st.epoch, res.At)
	if err != nil {
		return res, fmt.Errorf("%w: advancing the sprint's epoch: %v; run: nova-sprint clear again", ErrUnknown, err)
	}
	if !ok {
		return res, fmt.Errorf("the sprint left epoch %d while it was cleared (another clear); nothing was changed by this one; run: nova-sprint where", st.epoch)
	}
	next, _, err := st.repinOnly(ctx)
	if err != nil {
		return res, err
	}
	if next.epoch != res.To {
		// Another clear advanced past this one's epoch, having performed the
		// restore this one owed first.
		return res, fmt.Errorf("the sprint left epoch %d as it was restored (another clear); run: nova-sprint where", res.To)
	}
	snap, err := next.restore(ctx, res.From)
	if err != nil {
		return res, err
	}
	res.Held = map[string]int{"primaries": placed(snap.Work, ""), "work cards": placed(snap.Fleet, sprint.Ctl),
		"read cards": placed(snap.Readers, ""), "merge cards": placed(snap.Merge, sprint.Ctl), "open judgments": len(snap.Open)}
	return res, nil
}

func placed(t *sprint.Table, except string) int {
	n := 0
	for _, c := range t.Cards {
		if c.Placed() && c.Col != except {
			n++
		}
	}
	return n
}

// restore performs the restore the pinned epoch owes: it reads epoch from,
// frozen since the advance (every write to it is refused as stale), and
// writes its shape at the pinned epoch: the rows of the four tables, then the
// control cards (one step, holding the pinned epoch), then the display cells;
// it removes a fence the old epoch still holds (its writer is refused as
// stale at its next write), and records the restore done. It is the old
// epoch as read.
func (st *Store) restore(ctx context.Context, from uint64) (*sprint.Snapshot, error) {
	old := st.At(from)
	snap, err := old.Load(ctx, All, nil)
	if err != nil {
		return nil, fmt.Errorf("reading epoch %d to restore its shape: %w", from, err)
	}
	sh := sprint.ShapeOf(snap)
	for t, rows := range map[string][]string{sprint.Work: sh.Streams, sprint.Merge: sh.Streams, sprint.Readers: sh.Readers, sprint.Fleet: sh.Members} {
		if len(rows) == 0 {
			continue
		}
		if err := st.B.RowsAdd(ctx, st.Names.Table(t), rows); err != nil {
			return nil, fmt.Errorf("the rows of %s: %w", st.Names.Table(t), err)
		}
	}
	at := st.epoch
	res, err := st.Run(ctx, Step{Verb: "clear", Load: All, Mirrors: true, Epoch: &at,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.RestoreShape(s, sh) }})
	if err != nil {
		return nil, err
	}
	if len(res.Refused) > 0 {
		return nil, fmt.Errorf("the control cards: %s: %s", res.Refused[0].Key, res.Refused[0].Why)
	}
	f, err := old.B.ReadFence(ctx)
	if err != nil {
		return nil, err
	}
	if f.Pending != nil {
		if err := old.B.Release(ctx, *f.Pending, false); err != nil {
			return nil, fmt.Errorf("removing the fence of epoch %d (operation %s): %w", from, f.Pending.ID, err)
		}
	}
	return snap, st.root.SettleEpoch(ctx, at)
}
