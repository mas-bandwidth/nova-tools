package refmodel_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
)

// Today's bounds, stated as numbers: one part of the tick moves at most 2,000
// units (one table-layer write's candidates, errata 3 amendment 10) and writes at
// most 50 judgments, and what is left is due, a count in
// one move. The fixtures do not read the constants of the tick, so a change to
// one of them is a change of what the reference says, and fails here.

// countKinds is how many moves there are of a kind, and the due moves.
func countKinds(ms []refmodel.Move, kind string) (n int, due []refmodel.Move) {
	for _, m := range ms {
		switch m.Kind {
		case kind:
			n++
		case refmodel.KindDue:
			due = append(due, m)
		}
	}
	return n, due
}

func TestResolveMovesTwoThousandAndLeavesTheRestDue(t *testing.T) {
	t.Parallel()
	for waiters, want := range map[int]struct {
		moved int
		due   string // the attr of the due move, "" for none
	}{
		2000: {2000, ""},
		2001: {2000, "due=1"},
		2030: {2000, "due=30"},
	} {
		w := sprintOf(t, "m1")
		w.add(t, "s1", 1)
		w.must(t, sprint.Add(w.s, sprint.AddReq{Stream: "s2", Count: waiters, Needs: []string{"s1-1"}, Who: coordinator}))
		w.drive(t, "s1-1", sprint.Merging)
		w.land(t, "s1-1")
		got := refmodel.ResolveMoves(w.snapshot(nil), later(0))
		moved, due := countKinds(got, refmodel.KindMove)
		switch {
		case moved != want.moved:
			t.Errorf("%d waiters: %d moved, want %d", waiters, moved, want.moved)
		case want.due == "" && len(due) != 0:
			t.Errorf("%d waiters: something is due:%s", waiters, show(due))
		case want.due != "" && (len(due) != 1 || !slices.Equal(due[0].Attrs, []string{want.due})):
			t.Errorf("%d waiters: the due move is %s, want %s", waiters, show(due), want.due)
		}
		// the oldest are the ones moved: the last waiter is left for the next tick
		last := fmt.Sprintf("s2-%d", waiters)
		left := !slices.ContainsFunc(got, func(m refmodel.Move) bool { return m.Card == last })
		if left != (waiters > 2000) {
			t.Errorf("%d waiters: %s left for the next tick is %v", waiters, last, left)
		}
	}
}

// The judgments of a part have no bound but the step's: every judgment a part
// finds is written in its tick, none left due (the owner's rule: never a row
// at a time).
func TestDeadlinesWriteEveryJudgmentTheyFindInTheTick(t *testing.T) {
	t.Parallel()
	for late, want := range map[int]struct {
		opened int
		due    string
	}{
		50: {50, ""},
		51: {51, ""},
		60: {60, ""},
	} {
		w := sprintOf(t, "m1")
		w.add(t, "s1", late)
		for i := 1; i <= late; i++ {
			w.deal(t, fmt.Sprintf("s1-%d", i))
		}
		got := refmodel.DeadlineMoves(w.snapshot(nil), later(15*time.Minute+time.Second))
		opened, due := countKinds(got, refmodel.KindOpen)
		switch {
		case opened != want.opened:
			t.Errorf("%d late cards: %d judgments written, want %d", late, opened, want.opened)
		case want.due == "" && len(due) != 0:
			t.Errorf("%d late cards: something is due:%s", late, show(due))
		case want.due != "" && (len(due) != 1 || !slices.Equal(due[0].Attrs, []string{want.due})):
			t.Errorf("%d late cards: the due move is %s, want %s", late, show(due), want.due)
		}
	}
}

// Running time counts the time STOPPED after the card was dealt and no more of
// a span than that: a span that began before the card was dealt counts from the
// deal.
func TestDeadlinesCountOnlyTheTimeStoppedAfterTheCardWasDealt(t *testing.T) {
	t.Parallel()
	w := sprintOf(t, "m1")
	w.add(t, "s1", 1)
	w.deal(t, "s1-1") // dealt at t0, by hand, while the machine was STOPPED
	snap := w.snapshot(nil)
	snap.Stopped = []sprint.Span{{From: t0.Add(-time.Hour), To: t0.Add(2 * time.Minute)}}
	// two of the first 17 minutes were STOPPED: 15 have run, and one second more is past the deadline
	expect(t, refmodel.DeadlineMoves(snap, later(17*time.Minute)))
	expect(t, refmodel.DeadlineMoves(snap, later(17*time.Minute+time.Second)),
		"open a work card is past its deadline [s1-1]")
}
