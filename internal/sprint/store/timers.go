package store

import (
	"context"
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

// timerStep runs one change of the timer record as a step of its own: plan
// reads the record under the step's fence (Step.Timers) and its commit
// applies the change to the record as the commit finds it (OpRecord.Timers),
// so a set, a cancel and the tick's timer duty are each one fenced step and
// none overwrites another's (tla/Timer.tla). A plan that returns an error
// writes nothing.
func (st *Store) timerStep(ctx context.Context, verb string, plan func(t sprint.Timers) (sprint.TimerChange, error)) error {
	var perr error
	_, err := st.Run(ctx, Step{Verb: verb, Timers: true, Plan: func(s *sprint.Snapshot) sprint.Plan {
		c, err := plan(s.Timers)
		if perr = err; err != nil {
			return sprint.Plan{}
		}
		return sprint.Plan{Timers: &c}
	}})
	if err != nil {
		return err
	}
	return perr
}

// AddTimer writes one timer to the record and returns it as stored, with its
// id. The tick of a RUNNING machine raises it once its due time has come.
func (st *Store) AddTimer(ctx context.Context, t sprint.Timer) (sprint.Timer, error) {
	t, err := st.NewTimer(t)
	if err != nil {
		return sprint.Timer{}, err
	}
	err = st.timerStep(ctx, "remind", func(sprint.Timers) (sprint.TimerChange, error) {
		return sprint.TimerChange{Add: &t}, nil
	})
	return t, err
}

// NewTimer is the timer AddTimer would write, with its id, by the store's
// actor, set now; the refusal it would give, writing nothing (--dry-run).
func (st *Store) NewTimer(t sprint.Timer) (sprint.Timer, error) {
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
	return t, nil
}

// OpenTimer is the open timer id names; a refusal when there is none.
func (st *Store) OpenTimer(ctx context.Context, id string) (sprint.Timer, error) {
	t, err := st.Timers(ctx)
	if err != nil {
		return sprint.Timer{}, err
	}
	return openTimer(t, id)
}

func openTimer(t sprint.Timers, id string) (sprint.Timer, error) {
	i := t.Find(id)
	if i < 0 {
		return sprint.Timer{}, fmt.Errorf("no open timer is %s; run: nova-sprint remind --list", id)
	}
	return t.Open[i], nil
}

// CancelTimer takes one timer off the record and returns it; a timer the tick
// raised is no longer open, so there is none to cancel. The cancel is a
// fenced step: a tick that read the timer before it commits loses its try and
// reads again, so a cancelled timer is never raised (tla/Timer.tla,
// CancelledNeverFires).
func (st *Store) CancelTimer(ctx context.Context, id string) (sprint.Timer, error) {
	var out sprint.Timer
	err := st.timerStep(ctx, "remind cancel", func(t sprint.Timers) (sprint.TimerChange, error) {
		var err error
		if out, err = openTimer(t, id); err != nil {
			return sprint.TimerChange{}, err
		}
		return sprint.TimerChange{Close: []string{id}}, nil
	})
	if err != nil {
		return sprint.Timer{}, err
	}
	return out, nil
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
	if len(sprint.DueTimers(t, st.now(), m.StoppedBetween)) == 0 {
		return nil // none due: no step (the step reads the record again under its fence)
	}
	var raised []sprint.Timer
	r, err := st.Run(ctx, timerTick(m, &raised))
	if err != nil {
		return err
	}
	if r.Op == "" {
		return nil // nothing was due when the step read, or it lost every try
	}
	part := PartResult{Name: "timers", Result: Result{Verb: "tick timers"}}
	for _, x := range raised {
		part.Moved = append(part.Moved, fmt.Sprintf("TIMER %s to %s: %s", x.ID, x.For, x.Note))
	}
	part.Notes = r.Notes
	res.Parts = append(res.Parts, part)
	return nil
}

// timerTick is the tick's timer step: it plans on the record read under its
// own fence (Step.Timers), each try afresh, so a cancel or a set committed
// after an earlier try's read is what the try that commits sees; raised is
// what that try planned.
func timerTick(m Machine, raised *[]sprint.Timer) Step {
	return Step{Verb: "tick timers", Actor: sprint.MachineActor, Timers: true, Plan: func(s *sprint.Snapshot) sprint.Plan {
		*raised = sprint.DueTimers(s.Timers, s.Now, m.StoppedBetween)
		return sprint.TimerNotes(s, m.StoppedBetween, sprint.MachineActor)
	}}
}
