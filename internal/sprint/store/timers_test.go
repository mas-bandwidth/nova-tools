package store

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The timer duty on the twin store, its clock stepped by hand: no sleeps
// (docs/SPEC-SPRINT.md, "Timers"; tla/Timer.tla).

// setTimer writes one timer due at t0+d, as the remind verb does.
func (h *harness) setTimer(d time.Duration, forWho string) sprint.Timer {
	h.t.Helper()
	tm, err := h.st.AddTimer(h.ctx, sprint.Timer{For: forWho, Due: h.now.Add(d), Note: "the merge window closes"})
	require.NoError(h.t, err, "remind: %v", err)
	return tm
}

// timers is the open timer record the tick reads and the step leaves.
func (h *harness) timers() sprint.Timers {
	h.t.Helper()
	ts, err := h.st.Timers(h.ctx)
	require.NoError(h.t, err, "timers: %v", err)
	return ts
}

// reopened is the store reopened over the same record: the machine's memory
// is gone, the store is kept (tla/Timer.tla, Restart).
func (h *harness) reopened() *Store {
	h.t.Helper()
	return &Store{B: h.m, Names: h.st.Names, Actor: h.st.Actor, Now: h.st.Now, NewID: h.st.NewID,
		Sleep: func(time.Duration) {}}
}

func TestRemindTimerFiresOnceAtItsTimeThroughTheTick(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()
	want := h.setTimer(30*time.Minute, "coordinator")

	// Not before its due time.
	h.tick(29 * time.Minute)
	h.machine()
	assert.Empty(t, h.openOf(sprint.NTimer), "raised before its due time")
	require.Len(t, h.timers().Open, 1, "the timer is still open")

	// Once, at its due time: one judgment of kind "timer" addressed to its
	// actor, and the timer closed in the same step.
	h.tick(time.Minute)
	res := h.machine()
	open := h.openOf(sprint.NTimer)
	require.Len(t, open, 1, "one judgment at its due time")
	n := open[0].Note
	assert.Equal(t, sprint.Judgment, n.Kind, "a judgment")
	assert.Equal(t, sprint.NTimer, n.Type, "of kind timer")
	assert.Equal(t, "coordinator", n.To, "addressed to its actor")
	assert.Equal(t, sprint.TimerSubject(want.ID), open[0].Subject(), "open on its own subject")
	assert.Contains(t, n.What, "the merge window closes", "carrying its note")
	assert.Empty(t, h.timers().Open, "the timer is closed in the same step")
	var moved []string
	for _, p := range res.Parts {
		if p.Name == "timers" {
			moved = append(moved, p.Moved...)
		}
	}
	require.Len(t, moved, 1, "the duty's line: %+v", res.Parts)
	assert.Contains(t, moved[0], "TIMER "+want.ID+" to coordinator", "the duty's line")

	// Never again on a later tick.
	h.tick(time.Hour)
	h.machine()
	assert.Len(t, h.openOf(sprint.NTimer), 1, "a second judgment on a later tick")
	assert.Equal(t, 1, h.notesOf(sprint.NTimer), "a second note written")
}

func TestRemindTimerSurvivesARestart(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()
	want := h.setTimer(30*time.Minute, "coordinator")

	// The store is reopened: the machine's memory is lost and the record is
	// kept, so the timer is still raised at its due time.
	st := h.reopened()
	h.tick(30 * time.Minute)
	_, err := st.Tick(h.ctx)
	require.NoError(t, err, "the reopened store's tick: %v", err)
	open := h.openOf(sprint.NTimer)
	require.Len(t, open, 1, "the timer fired after the restart")
	assert.Equal(t, sprint.TimerSubject(want.ID), open[0].Subject())
	assert.Empty(t, h.timers().Open, "closed in the same step")
}

func TestRemindTimerCancelStopsIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()
	gone := h.setTimer(30*time.Minute, "coordinator")
	keep := h.setTimer(30*time.Minute, "friend-a")

	c, err := h.st.CancelTimer(h.ctx, gone.ID)
	require.NoError(t, err, "cancel: %v", err)
	assert.Equal(t, gone.ID, c.ID, "the timer it cancelled")
	ts := h.timers()
	require.Len(t, ts.Open, 1, "one timer left")
	assert.Equal(t, keep.ID, ts.Open[0].ID, "the other is kept")

	h.tick(30 * time.Minute)
	h.machine()
	open := h.openOf(sprint.NTimer)
	require.Len(t, open, 1, "only the open timer fired")
	assert.Equal(t, sprint.TimerSubject(keep.ID), open[0].Subject(), "the cancelled timer fired")

	// A timer that fired is off the record, so there is none to cancel.
	_, err = h.st.CancelTimer(h.ctx, keep.ID)
	require.Error(t, err, "cancel a fired timer")
	assert.Contains(t, err.Error(), "no open timer is "+keep.ID, "the refusal names the id")
	_, err = h.st.CancelTimer(h.ctx, "none")
	require.Error(t, err, "cancel an unknown id")
	assert.Contains(t, err.Error(), "remind --list", "the refusal names the remedy")
}

func TestRemindTimerCountsRunningTimeAcrossAStop(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()
	h.setTimer(30*time.Minute, "coordinator")
	// The machine is STOPPED for the half hour the timer would have run out
	// in: a timer counts running time, so it is not due on the wall clock.
	h.stopMachine()
	h.tick(45 * time.Minute)
	h.machine()
	assert.Empty(t, h.openOf(sprint.NTimer), "due while the machine was STOPPED")
	require.Len(t, h.timers().Open, 1, "still open")
	h.startMachine()
	// Running again, the thirty minutes it wants are owed from the start.
	h.tick(29 * time.Minute)
	h.machine()
	assert.Empty(t, h.openOf(sprint.NTimer), "due before its running time passed")
	h.tick(time.Minute)
	h.machine()
	assert.Len(t, h.openOf(sprint.NTimer), 1, "due once its running time passed")
}

func TestRemindTimerRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, tc := range []struct {
		name string
		t    sprint.Timer
		want string
	}{
		{"no due time", sprint.Timer{For: "coordinator", Note: "x"}, "no due time"},
		{"a due time in the past", sprint.Timer{For: "coordinator", Note: "x", Due: h.now.Add(-time.Minute)}, "not after now"},
		{"no note", sprint.Timer{For: "coordinator", Due: h.now.Add(time.Minute)}, "the timer's note is empty"},
		{"an unsafe actor name", sprint.Timer{For: "../etc", Due: h.now.Add(time.Minute), Note: "x"}, "lower-case letters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.st.AddTimer(h.ctx, tc.t)
			require.Error(t, err, "accepted")
			assert.Contains(t, err.Error(), tc.want)
			assert.Empty(t, h.timers().Open, "wrote a timer it refused")
		})
	}
}

// writeDuringTick runs the tick's timer step (timerTick) with write run once
// between its first read of the record and its commit, by another writer: a
// coordinator writing while the tick's step is in flight.
func (h *harness) writeDuringTick(write func(other *Store)) Result {
	h.t.Helper()
	m, _, err := h.st.Machine(h.ctx)
	require.NoError(h.t, err)
	var raised []sprint.Timer
	step := timerTick(m, &raised)
	plan, other, done := step.Plan, h.reopened(), false
	step.Plan = func(s *sprint.Snapshot) sprint.Plan {
		p := plan(s)
		if !done {
			done = true
			write(other)
		}
		return p
	}
	res, err := h.st.Run(h.ctx, step)
	require.NoError(h.t, err, "the tick's timer step: %v", err)
	return res
}

func TestRemindTimerCancelledDuringATickNeverFires(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()
	gone := h.setTimer(30*time.Minute, "coordinator")
	h.tick(30 * time.Minute)

	// The tick's step read the timer due; a cancel commits before the step
	// does. The step's try loses the fence and reads again, so the cancelled
	// timer is not raised (tla/Timer.tla, CancelledNeverFires).
	h.writeDuringTick(func(other *Store) {
		_, err := other.CancelTimer(h.ctx, gone.ID)
		require.NoError(t, err, "the cancel: %v", err)
	})
	assert.Empty(t, h.openOf(sprint.NTimer), "a timer cancelled during the tick fired")
	assert.Empty(t, h.timers().Open, "the record after the cancel")
	h.tick(time.Hour)
	h.machine()
	assert.Empty(t, h.openOf(sprint.NTimer), "a cancelled timer fired on a later tick")
}

func TestRemindTimerSetDuringATickIsKept(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()
	due := h.setTimer(30*time.Minute, "coordinator")
	h.tick(30 * time.Minute)

	// A timer set while the tick's step raises another is on the record the
	// step leaves: the commit closes only the ids it raised, on the record as
	// the commit finds it (tla/Timer.tla, NoLapse).
	var later sprint.Timer
	h.writeDuringTick(func(other *Store) {
		var err error
		later, err = other.AddTimer(h.ctx, sprint.Timer{For: "friend-a", Due: h.now.Add(time.Hour), Note: "the later one"})
		require.NoError(t, err, "the set: %v", err)
	})
	open := h.openOf(sprint.NTimer)
	require.Len(t, open, 1, "the due timer is raised once")
	assert.Equal(t, sprint.TimerSubject(due.ID), open[0].Subject())
	ts := h.timers()
	require.Len(t, ts.Open, 1, "the timer set during the tick was lost: %+v", ts)
	assert.Equal(t, later.ID, ts.Open[0].ID)

	// and it fires in its own time
	h.tick(time.Hour)
	h.machine()
	assert.Len(t, h.openOf(sprint.NTimer), 2, "the timer set during the tick never fired")
}
