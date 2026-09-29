package store

import (
	"context"
	"encoding/json"
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
// STOPPED and leaves it STOPPED, then advances the sprint's
// epoch, once, atomically. Nothing is deleted: the old epoch stays where it is
// and readable (At), and every writer still holding it is refused by the
// table layer as stale. The new epoch starts with the same shape (streams,
// readers, members and their status) and no card: its rows and control cards
// are written at it, and the notifications, judgments, cursor and fence of the
// new epoch are its own, empty. A pending operation of the old epoch is
// finished first, or abandoned with the old epoch. A clear cut before it
// restored its shape is finished first by the next one.
func (st *Store) Clear(ctx context.Context) (ClearResult, error) {
	var res ClearResult
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
	st, err := st.repin(ctx)
	if err != nil {
		return res, err
	}
	es, err := st.EpochNow(ctx)
	if err != nil {
		return res, err
	}
	if es.Shape != "" {
		if err := st.restore(ctx, es.Shape); err != nil {
			return res, fmt.Errorf("finishing the clear of %s: %w", es.Cleared.UTC().Format(time.RFC3339), err)
		}
		res.Restored = true
	}
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
	snap, err := st.Load(ctx, All, nil)
	if err != nil {
		return res, err
	}
	res.Held = map[string]int{"primaries": placed(snap.Work, ""), "work cards": placed(snap.Fleet, sprint.Ctl),
		"read cards": placed(snap.Readers, ""), "merge cards": placed(snap.Merge, sprint.Ctl), "open judgments": len(snap.Open)}
	body, err := json.Marshal(sprint.ShapeOf(snap))
	if err != nil {
		return res, err
	}
	res.From, res.To, res.At = st.epoch, st.epoch+1, st.now()
	ok, err := st.root.AdvanceEpoch(ctx, st.epoch, res.At, string(body))
	if err != nil {
		return res, fmt.Errorf("%w: advancing the sprint's epoch: %v; run: nova-sprint clear again", ErrUnknown, err)
	}
	if !ok {
		return res, fmt.Errorf("the sprint left epoch %d while it was cleared (another clear); nothing was changed by this one; run: nova-sprint where", st.epoch)
	}
	next, err := st.repin(ctx)
	if err != nil {
		return res, err
	}
	return res, next.restore(ctx, string(body))
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

// restore writes a shape at the pinned epoch: the rows of the four tables,
// then the control cards (one step), then the display cells, and records the
// shape restored.
func (st *Store) restore(ctx context.Context, body string) error {
	var sh sprint.Shape
	if err := json.Unmarshal([]byte(body), &sh); err != nil {
		return fmt.Errorf("the shape to restore is unreadable: %w", err)
	}
	for t, rows := range map[string][]string{sprint.Work: sh.Streams, sprint.Merge: sh.Streams, sprint.Readers: sh.Readers, sprint.Fleet: sh.Members} {
		if len(rows) == 0 {
			continue
		}
		if err := st.B.RowsAdd(ctx, st.Names.Table(t), rows); err != nil {
			return fmt.Errorf("the rows of %s: %w", st.Names.Table(t), err)
		}
	}
	res, err := st.Run(ctx, Step{Verb: "clear", Load: All, Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.RestoreShape(s, sh) }})
	if err != nil {
		return err
	}
	if len(res.Refused) > 0 {
		return fmt.Errorf("the control cards: %s: %s", res.Refused[0].Key, res.Refused[0].Why)
	}
	return st.root.SettleEpoch(ctx)
}
