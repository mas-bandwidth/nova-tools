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
		p := TimerNotes(&Snapshot{Now: at}, rec, DueTimers(rec, at, nil), MachineActor)
		assert.True(t, p.Empty(), "raised at %s, before its due time", at)
	}

	// Once, at its due time: one judgment of kind NTimer addressed to its
	// actor, and the record the step leaves has the timer closed.
	p := TimerNotes(&Snapshot{Now: due}, rec, DueTimers(rec, due, nil), MachineActor)
	require.Len(t, p.Notes, 1, "the tick at its due time")
	n := p.Notes[0]
	assert.Equal(t, Judgment, n.Kind, "the note is a judgment")
	assert.Equal(t, NTimer, n.Type, "of kind timer")
	assert.Equal(t, "coordinator", n.To, "addressed to its actor")
	assert.Equal(t, []string{TimerSubject("t1")}, n.Primaries, "open on its own subject")
	assert.Contains(t, n.What, "the merge window closes", "carrying its note")
	assert.Equal(t, []string{"ack"}, n.Decisions, "its decisions")
	require.NotNil(t, p.Timers, "the step closes the timer")
	assert.Empty(t, p.Timers.Open, "the record the step leaves")

	// Never again on a later tick: the timer the step closed is not there to
	// raise, so no later tick writes a second judgment.
	left := *p.Timers
	for _, at := range []time.Time{due, due.Add(time.Hour), due.Add(24 * time.Hour)} {
		assert.True(t, TimerNotes(&Snapshot{Now: at}, left, DueTimers(left, at, nil), MachineActor).Empty(),
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
	i := two.Find("t1")
	require.Equal(t, 0, i)
	two.Open = append(two.Open[:i], two.Open[i+1:]...) // the cancel takes it off the record
	p := TimerNotes(&Snapshot{Now: due}, two, DueTimers(two, due, nil), MachineActor)
	require.Len(t, p.Notes, 1, "only the open timer is raised")
	assert.Equal(t, "t2", p.Notes[0].Primaries[0][len("timer:"):], "the cancelled timer fired")
	// A timer the tick raised is off the record, so it is not there to cancel:
	// Find says none, and a second raise is impossible.
	raised := TimerNotes(&Snapshot{Now: due}, rec, DueTimers(rec, due, nil), MachineActor)
	require.NotNil(t, raised.Timers)
	assert.Equal(t, -1, raised.Timers.Find("t1"), "a fired timer is no longer open")
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
			assert.Equal(t, tc.want, timer, "the timer")
			assert.Equal(t, tc.want, review, "the review time")
			assert.Equal(t, review, timer, "one due check answers both")
		})
	}
}
