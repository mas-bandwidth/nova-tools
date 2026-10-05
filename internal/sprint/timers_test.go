package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The timer's pure rules (docs/SPEC-SPRINT.md, "Timers"; tla/Timer.tla), on a
// clock stepped by hand: no sleeps.

var timerT0 = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

// timerRecord is one open timer, written at timerT0 and due 30 minutes later.
func timerRecord(forWho string) (Timers, time.Time) {
	due := timerT0.Add(30 * time.Minute)
	return Timers{Open: []Timer{{ID: "t1", For: forWho, Due: due, Note: "the merge window closes",
		By: "coordinator", Set: timerT0}}}, due
}

func TestRemindTimerFiresOnceAtItsTime(t *testing.T) {
	t.Parallel()
	rec, due := timerRecord("coordinator")

	// Not before its due time.
	for _, at := range []time.Time{timerT0, timerT0.Add(time.Minute), due.Add(-time.Nanosecond)} {
		p := TimerNotes(&Snapshot{Now: at, Timers: rec}, nil, MachineActor)
		assert.True(t, p.Empty(), "raised at %s, before its due time", at)
	}

	// Once, at its due time: one judgment of kind NTimer addressed to its
	// actor, and the step's write closes the timer.
	p := TimerNotes(&Snapshot{Now: due, Timers: rec}, nil, MachineActor)
	require.Len(t, p.Notes, 1, "the tick at its due time")
	n := p.Notes[0]
	assert.Equal(t, Judgment, n.Kind, "the note is a judgment")
	assert.Equal(t, NTimer, n.Type, "of kind timer")
	assert.Equal(t, "coordinator", n.To, "addressed to its actor")
	assert.Equal(t, []string{TimerSubject("t1")}, n.Primaries, "open on its own subject")
	assert.Contains(t, n.What, "the merge window closes", "carrying its note")
	assert.Equal(t, []string{"ack"}, n.Decisions, "its decisions")
	require.NotNil(t, p.Timers, "the step closes the timer")
	assert.Equal(t, []string{"t1"}, p.Timers.Close, "the step closes the timer it raised")
	assert.Empty(t, p.Timers.Add, "and sets none")
	left := p.Timers.Apply(rec)
	assert.Empty(t, left.Open, "the record the commit leaves")

	// Never again on a later tick: the timer the step closed is not there to
	// raise, so no later tick writes a second judgment.
	for _, at := range []time.Time{due, due.Add(time.Hour), due.Add(24 * time.Hour)} {
		assert.True(t, TimerNotes(&Snapshot{Now: at, Timers: left}, nil, MachineActor).Empty(),
			"raised again at %s", at)
	}
}

func TestRemindTimerCountsRunningTimeNotWallTime(t *testing.T) {
	t.Parallel()
	rec, due := timerRecord("friend-a")
	// The machine was STOPPED for the twenty minutes before the due time: a
	// timer counts running time, as every deadline of the tree does, so the
	// stopped span is owed after the wall clock passed the due time.
	stopped := func(from, to time.Time) time.Duration {
		return StoppedBetween([]Span{{From: timerT0.Add(10 * time.Minute), To: timerT0.Add(30 * time.Minute)}}, from, to)
	}
	assert.Empty(t, DueTimers(rec, due, stopped), "due at its wall time with twenty minutes stopped")
	assert.Empty(t, DueTimers(rec, due.Add(19*time.Minute), stopped), "nineteen minutes of the stopped span owed")
	got := DueTimers(rec, due.Add(20*time.Minute), stopped)
	require.Len(t, got, 1, "due once the stopped span is owed")
	assert.Equal(t, "t1", got[0].ID)
}

func TestRemindTimerCancelStopsItAndATimerRaisedIsNotThereToCancel(t *testing.T) {
	t.Parallel()
	rec, due := timerRecord("coordinator")
	// A cancel takes the timer off the record, so the tick at its due time
	// raises nothing (tla/Timer.tla, Cancel and CancelledNeverFires).
	two := Timers{Open: []Timer{rec.Open[0], {ID: "t2", For: "friend-a", Due: due, Note: "the second",
		By: "coordinator", Set: timerT0}}}
	c := CancelTimer(&Snapshot{Now: timerT0, Timers: two}, "t1")
	require.Empty(t, c.Refused)
	require.NotNil(t, c.Timers)
	two = c.Timers.Apply(two) // the cancel's commit takes it off the record
	p := TimerNotes(&Snapshot{Now: due, Timers: two}, nil, MachineActor)
	require.Len(t, p.Notes, 1, "only the open timer is raised")
	assert.Equal(t, "t2", p.Notes[0].Primaries[0][len("timer:"):], "the cancelled timer fired")
	// A timer the tick raised is off the record, so it is not there to cancel:
	// the cancel planned on the record after the tick is refused and writes
	// nothing.
	raised := TimerNotes(&Snapshot{Now: due, Timers: rec}, nil, MachineActor)
	require.NotNil(t, raised.Timers)
	after := raised.Timers.Apply(rec)
	assert.Equal(t, -1, after.Find("t1"), "a fired timer is no longer open")
	late := CancelTimer(&Snapshot{Now: due, Timers: after}, "t1")
	assert.Len(t, late.Refused, 1, "a cancel of a raised timer is refused")
	assert.Nil(t, late.Timers, "and writes nothing")
}

// TestRemindTimerWriteKeepsWhatAnotherStepWrote pins the commit's rule: a
// step's change to the timer record is applied to the record as the commit
// reads it, never a copy the step read before. A tick that planned on a record
// holding t1 alone commits after a cancel of t1 and a set of t3: t1 is not
// put back, t3 is not lost (tla/Timer.tla, CancelledNeverFires and NoLapse).
func TestRemindTimerWriteKeepsWhatAnotherStepWrote(t *testing.T) {
	t.Parallel()
	rec, due := timerRecord("coordinator")
	tick := TimerNotes(&Snapshot{Now: due, Timers: rec}, nil, MachineActor)
	require.NotNil(t, tick.Timers)

	t3 := Timer{ID: "t3", For: "friend-a", Due: due.Add(time.Hour), Note: "later", By: "friend-a", Set: due}
	now := SetTimer(t3).Timers.Apply(rec) // a set between the tick's read and its commit
	now = tick.Timers.Apply(now)
	assert.Equal(t, -1, now.Find("t1"), "the raised timer is closed")
	assert.Equal(t, 0, now.Find("t3"), "the timer set meanwhile is kept")

	// applied twice (a writer finishing another's operation), as once
	assert.Equal(t, now, tick.Timers.Apply(now))
	assert.Equal(t, now, SetTimer(t3).Timers.Apply(now))
}

// TestRemindTimerAndTheReviewTimeShareOneDueCheck pins the one mechanism: the
// timer's due test and the judgment review time wait set are the same
// function, DueNow, so they answer alike at every clock reading and every
// stopped span, and a later external wait operand (`after <time>`) has no
// second clock comparison to disagree with.
func TestRemindTimerAndTheReviewTimeShareOneDueCheck(t *testing.T) {
	t.Parallel()
	set := timerT0
	due := timerT0.Add(30 * time.Minute)
	rec, _ := timerRecord("coordinator")
	spans := []Span{{From: timerT0.Add(5 * time.Minute), To: timerT0.Add(15 * time.Minute)}}
	stopped := func(from, to time.Time) time.Duration { return StoppedBetween(spans, from, to) }

	for _, tc := range []struct {
		name string
		now  time.Time
		want bool
	}{
		{"before the due time", timerT0.Add(20 * time.Minute), false},
		{"at the wall due time, ten minutes stopped", due, false},
		{"one minute short of the stopped span owed", due.Add(9 * time.Minute), false},
		{"the stopped span owed", due.Add(10 * time.Minute), true},
		{"long after", due.Add(time.Hour), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// the timer's tick
			timer := len(DueTimers(rec, tc.now, stopped)) == 1
			// the judgment's review time, through the tick's overdue part
			n := Note{ID: "n1", Kind: Judgment, Type: NRed, Primaries: []string{"c1"}, Count: 1,
				At: set, Review: due, ReviewSet: set}
			s := &Snapshot{Now: tc.now, Open: []Open{{Key: OpenKey("n1", "c1"), Note: n}}}
			p, _ := TickOverdue(s, TickReq{Who: MachineActor, Stopped: stopped})
			review := false
			for _, x := range p.Notes {
				review = review || x.Type == NOverdue
			}
			// the inbox's overdue mark of the same judgment
			_, inbox := InboxReq{Now: tc.now, Stopped: stopped}.due(n)
			// the no-stall rule's judgment past its due time
			c := &held{s: s, req: TickReq{Who: MachineActor, Stopped: stopped}, marks: map[string]bool{}}
			stall := len(c.overdueUnmarked()) == 1
			assert.Equal(t, tc.want, timer, "the timer")
			assert.Equal(t, tc.want, review, "the review time")
			assert.Equal(t, tc.want, inbox, "the inbox's review time")
			assert.Equal(t, tc.want, stall, "the no-stall rule's review time")
			assert.Equal(t, review, timer, "one due check answers both")
		})
	}
}
