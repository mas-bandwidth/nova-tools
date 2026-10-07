package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The timer duty of the tick (docs/SPEC-SPRINT.md, "Timers"; tla/Timer.tla):
// the sprint's open timers are one record in the store, written by the remind
// verb and carried across a clear; the tick reads it and while the machine is
// RUNNING raises each timer whose due time the clock has reached as one
// judgment addressed to its actor, closing the timer in the same step, so a
// timer is raised once.

// keyTimers is the timer record (JSON): the sprint's open timers.
const keyTimers = "timers"

// Timers reads the timer record; none is empty.
func (st *Store) Timers(ctx context.Context) (sprint.Timers, error) {
	var t sprint.Timers
	err := st.getJSON(ctx, keyTimers, &t)
	return t, err
}

// updateTimers takes the fence as a lock (the same lock a step holds across its
// plan and commit), reads the timer record fresh, applies fn and writes it back in
// due order, so a timer set or cancelled while a tick was running or committing is
// not lost.
func (st *Store) updateTimers(ctx context.Context, verb string, fn func(*sprint.Timers) error) error {
	pinned, err := st.pin(ctx)
	if err != nil {
		return err
	}
	r := pinned.retry(ctx)
	for r.next(pinned.attempts()) {
		_, gen, err := pinned.Fenced(ctx, nil, nil, nil)
		if err != nil {
			return err
		}
		lock := OpRecord{ID: "timers-" + pinned.newID() + "-lock", Verb: verb + " lock", At: pinned.now(), Lock: true}
		ok, err := pinned.B.Acquire(ctx, gen, lock)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		t, err := pinned.Timers(ctx)
		if err == nil {
			if err = fn(&t); err == nil {
				t.Sort()
				err = pinned.putJSON(ctx, keyTimers, t)
			}
		}
		return errors.Join(err, pinned.B.Release(ctx, lock, false))
	}
	return fmt.Errorf("%s: the sprint kept changing under it (%d tries); nothing was changed; run it again", verb, r.tries)
}

// AddTimer writes one timer to the record and returns it as stored, with its
// id. The tick of a RUNNING machine raises it once its due time has come.
func (st *Store) AddTimer(ctx context.Context, t sprint.Timer) (sprint.Timer, error) {
	if err := sprint.ValidGoalName(t.For); err != nil {
		return sprint.Timer{}, err
	}
	if err := sprint.ValidTimerNote(t.Note); err != nil {
		return sprint.Timer{}, err
	}
	now := st.now()
	t.ID, t.By, t.Set = st.newID(), st.Actor, now
	if t.Due.IsZero() {
		return sprint.Timer{}, fmt.Errorf("the timer has no due time; give --in <duration> or --at <time>")
	}
	if !t.Due.After(now) {
		return sprint.Timer{}, fmt.Errorf("the timer's due time %s is not after now %s; give --in <duration> or an --at in the future",
			t.Due.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}
	err := st.updateTimers(ctx, "add timer", func(ts *sprint.Timers) error {
		ts.Open = append(ts.Open, t)
		return nil
	})
	return t, err
}

// CancelTimer takes one timer off the record and returns it; a timer the tick
// raised is no longer open, so there is none to cancel.
func (st *Store) CancelTimer(ctx context.Context, id string) (sprint.Timer, error) {
	var out sprint.Timer
	err := st.updateTimers(ctx, "cancel timer", func(ts *sprint.Timers) error {
		i := ts.Find(id)
		if i < 0 {
			return fmt.Errorf("no open timer is %s; run: nova-sprint remind --list", id)
		}
		out = ts.Open[i]
		ts.Open = append(ts.Open[:i], ts.Open[i+1:]...)
		return nil
	})
	return out, err
}

// timers is the tick's timer duty: one step per tick that raises every timer
// whose due time has come and leaves the record without them, so a timer is
// raised once (tla/Timer.tla, Tick and AtMostOnce). The due test is the
// tree's one clock comparison (sprint.DueNow), so a timer counts running
// time: the time the machine was STOPPED since the timer was set does not
// count, as it does not for a judgment's review time.
func (st *Store) timers(ctx context.Context, m Machine, res *TickResult) error {
	t, err := st.Timers(ctx)
	if err != nil {
		return err
	}
	if len(t.Open) == 0 {
		return nil
	}
	due := sprint.DueTimers(t, st.now(), m.StoppedBetween)
	if len(due) == 0 {
		return nil
	}
	var moved []string
	r, err := st.Run(ctx, Step{Verb: "tick timers", Actor: sprint.MachineActor, Plan: func(s *sprint.Snapshot) sprint.Plan {
		cur, err := st.Timers(ctx)
		if err != nil {
			return sprint.Plan{}
		}
		p := sprint.TimerNotes(s, cur, due, sprint.MachineActor)
		moved = nil
		for _, x := range due {
			if cur.Find(x.ID) >= 0 {
				moved = append(moved, fmt.Sprintf("TIMER %s to %s: %s", x.ID, x.For, x.Note))
			}
		}
		return p
	}})
	if err != nil {
		return err
	}
	if r.Notes == 0 {
		return nil
	}
	part := PartResult{Name: "timers", Result: Result{Verb: "tick timers"}}
	part.Moved = moved
	part.Notes = r.Notes
	res.Parts = append(res.Parts, part)
	return nil
}
