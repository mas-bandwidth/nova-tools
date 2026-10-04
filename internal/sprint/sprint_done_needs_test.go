package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The sprint is done: an add that only opens a stream admits no card and
// leaves it done; "the sprint is done" is a happened note, never a judgment,
// so it has no due time and is never overdue (errata 3 amendment 6).
func TestSprintDoneOutlastsAnAddOfNoCardAndIsNeverOverdue(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	accepted(w, "s1-1")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1"}))
	w.must(Add(w.s, AddReq{Stream: "s2"}))
	p, _ := TickDone(w.s, TickReq{})
	require.NotNil(t, w.s.StreamCtl("s2"), "an add that only opens a stream: s2 %v, done %+v", w.s.StreamCtl("s2") != nil, p)
	require.Len(t, p.Notes, 1, "an add that only opens a stream: s2 %v, done %+v", w.s.StreamCtl("s2") != nil, p)
	w.must(tickDone(w.s, TickReq{}))
	for _, g := range Inbox(InboxReq{Now: w.s.Now.Add(1000 * time.Hour), Open: w.s.Open, Recent: w.notes, Deadline: time.Minute}) {
		if g.Type == NSprintDone {
			require.Equal(t, Happened, g.Kind, "the sprint is done, shown as a judgment or overdue: %+v", g)
			require.False(t, g.Overdue, "the sprint is done, shown as a judgment or overdue: %+v", g)
			require.False(t, g.Marked, "the sprint is done, shown as a judgment or overdue: %+v", g)
			require.True(t, g.Due.IsZero(), "the sprint is done, shown as a judgment or overdue: %+v", g)
			require.Empty(t, g.Decisions, "the sprint is done, shown as a judgment or overdue: %+v", g)
		}
	}
	require.Empty(t, w.s.Open, "the sprint done opened a judgment: %+v", w.s.Open)
	w.must(Add(w.s, AddReq{Stream: "s2", Count: 1}))
	p, _ = TickDone(w.s, TickReq{})
	require.True(t, p.Empty(), "an add of a card left the sprint done")
}

// Every primary dropped, none landed: the sprint is done, 0 landed.
func TestSprintDoneWithNothingLanded(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1", "s1-2"}}, Reason: "obsolete"}))
	w.must(tickDone(w.s, TickReq{}))
	done := w.notesOf(NSprintDone)
	require.Len(t, done, 1, "all dropped: %+v", done)
	require.Equal(t, "0 landed, 2 dropped", done[0].What, "all dropped: %+v", done)
	require.Empty(t, w.s.Open, "all dropped: %+v", done)
}

// A sprint that never had a card is not done.
func TestAnEmptySprintIsNotDone(t *testing.T) {
	t.Parallel()
	w := setup(t, 0)
	p, _ := TickDone(w.s, TickReq{})
	require.True(t, p.Empty(), "an empty sprint is done: %+v", p)
}

// Dropping needed cards is refused unless Cascade is true.
func TestDropRefusesNeededCardsForAck(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1", "s1-2"}}))
	// dropping s1-1 without cascade should be refused
	p := Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "gone"})
	require.Len(t, p.Refused, 1, "drop should be refused: %+v", p)
	require.Contains(t, p.Refused[0].Why, "s1-1 is needed by b", "refusal should name dependant")
	require.Contains(t, p.Refused[0].Why, "--cascade", "refusal should suggest cascade")
	// dropping with cascade drops b too
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "gone", Cascade: true}))
	require.Equal(t, "", w.s.StateOf("s1-1"), "s1-1 should be off the table")
	require.Equal(t, "", w.s.StateOf("b"), "b should be off the table")
	// s1-2 should still be ready
	require.Equal(t, Ready, w.s.StateOf("s1-2"), "s1-2 should be ready")
	w.clean("dropped with cascade")
}

// tickDone is the done part's plan alone.
func tickDone(s *Snapshot, r TickReq) Plan {
	p, _ := TickDone(s, r)
	return p
}
