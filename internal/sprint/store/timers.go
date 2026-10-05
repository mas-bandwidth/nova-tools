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

// AddTimer writes one timer to the record and returns it as stored, with its
// id. The tick of a RUNNING machine raises it once its due time has come. The
// write is a step under the fence, as the tick's close is, and its commit adds
// the timer to the record as it stands then (sprint.SetTimer), so a set and a
// tick's close never overwrite each other.
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
	res, err := st.Run(ctx, Step{Verb: "remind", Named: true, Plan: func(*sprint.Snapshot) sprint.Plan { return sprint.SetTimer(t) }})
	if err != nil {
		return sprint.Timer{}, err
	}
	if len(res.Refused) > 0 {
		return sprint.Timer{}, errors.New(res.Refused[0].Why)
	}
	return t, nil
}

// OpenTimer is the open timer id, read from the record; one the tick raised,
// or none ever set, is refused as --cancel refuses it.
func (st *Store) OpenTimer(ctx context.Context, id string) (sprint.Timer, error) {
	t, err := st.Timers(ctx)
	if err != nil {
		return sprint.Timer{}, err
	}
	i := t.Find(id)
	if i < 0 {
		return sprint.Timer{}, noTimer(id)
	}
	return t.Open[i], nil
}

func noTimer(id string) error {
	return fmt.Errorf("no open timer is %s; run: nova-sprint remind --list", id)
}

// CancelTimer takes one timer off the record and returns it; a timer the tick
// raised is no longer open, so there is none to cancel. The step reads the
// record after the fence's generation and its commit takes the id off the
// record as it stands then (sprint.CancelTimer), so a tick that raised it
// first makes this a refusal, never a cancel reported for a timer that fired.
func (st *Store) CancelTimer(ctx context.Context, id string) (sprint.Timer, error) {
	var out sprint.Timer
	res, err := st.Run(ctx, Step{Verb: "remind cancel", Named: true, Timers: true, Plan: func(s *sprint.Snapshot) sprint.Plan {
		if i := s.Timers.Find(id); i >= 0 {
			out = s.Timers.Open[i]
		}
		return sprint.CancelTimer(s, id)
	}})
	if err != nil {
		return sprint.Timer{}, err
	}
	if len(res.Refused) > 0 {
		return sprint.Timer{}, noTimer(id)
	}
	return out, nil
}

// timers is the tick's timer duty: one step per tick that raises every timer
// whose due time has come and takes them off the record in its own commit, so
// a timer is raised once (tla/Timer.tla, Tick and AtMostOnce). The step plans
// on the record read after the fence's generation (Step.Timers) and its
// commit takes off only the ids it raised (sprint.TimerWrite), so a timer
// cancelled before it commits is not raised and one set meanwhile is kept
// (tla/Timer.tla, CancelledNeverFires and NoLapse). The due test is the
// tree's one clock comparison (sprint.DueNow), so a timer counts running
// time: the time the machine was STOPPED since the timer was set does not
// count, as it does not for a judgment's review time.
func (st *Store) timers(ctx context.Context, m Machine, res *TickResult) error {
	t, err := st.Timers(ctx)
	if err != nil {
		return err
	}
	// a cheap read first: a tick with no timer due takes no step
	if len(sprint.DueTimers(t, st.now(), m.StoppedBetween)) == 0 {
		return nil
	}
	var raised []string
	r, err := st.Run(ctx, Step{Verb: "tick timers", Actor: sprint.MachineActor, Timers: true, Plan: func(s *sprint.Snapshot) sprint.Plan {
		p := sprint.TimerNotes(s, m.StoppedBetween, sprint.MachineActor)
		raised = raised[:0]
		if p.Timers != nil {
			for _, id := range p.Timers.Close {
				x := s.Timers.Open[s.Timers.Find(id)]
				raised = append(raised, fmt.Sprintf("TIMER %s to %s: %s", x.ID, x.For, x.Note))
			}
		}
		return p
	}})
	if err != nil {
		return err
	}
	if len(raised) == 0 {
		return nil // the record the step planned on had none due: a cancel came first
	}
	part := PartResult{Name: "timers", Result: Result{Verb: "tick timers"}}
	part.Moved = raised
	part.Notes = r.Notes
	res.Parts = append(res.Parts, part)
	return nil
}
