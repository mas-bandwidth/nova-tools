package sprint

import (
	"strings"
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
	require.Empty(t, w.s.Open, "the sprint done opened a judgment")
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

// Acknowledging a blocked judgment waives only the needs it names; a need
// dropped after it was written is its own judgment.
func TestAckWaivesOnlyTheNeedsItsJudgmentNames(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1", "s1-2"}}))
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "gone"}))
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-2"}}, Reason: "gone too"}))
	open := w.openOn("b")
	require.Len(t, open, 2, "blocked judgments on b: %+v", open)
	require.Equal(t, "s1-1|s1-2", strings.Join(open[0].Note.Needs, ",")+"|"+strings.Join(open[1].Note.Needs, ","), "blocked judgments on b: %+v", open)
	// A resolve writes none again.
	w.do(Resolve(w.s, ResolveReq{}))
	require.Len(t, w.notesOf(NBlocked), 2, "blocked notes after resolve: %d", len(w.notesOf(NBlocked)))
	w.must(Ack(w.s, AckReq{Notes: []string{open[0].Note.ID}, Reason: "fine"}))
	b := w.s.Work.Card("b")
	require.Equal(t, Waiting, b.Col, "the first ack: %s waived=%q", b.Col, b.F("waived"))
	require.Equal(t, "s1-1", b.F("waived"), "the first ack: %s waived=%q", b.Col, b.F("waived"))
	w.must(Ack(w.s, AckReq{Notes: []string{open[1].Note.ID}, Reason: "fine too"}))
	b = w.s.Work.Card("b")
	require.Equal(t, Ready, b.Col, "the second ack: %s waived=%q", b.Col, b.F("waived"))
	require.Equal(t, "s1-1,s1-2", b.F("waived"), "the second ack: %s waived=%q", b.Col, b.F("waived"))
	needs, _ := NeedsOf(w.s, "b")
	require.Len(t, needs, 2, "needs of b: %+v", needs)
	require.True(t, needs[0].Waived, "needs of b: %+v", needs)
	require.True(t, needs[1].Waived, "needs of b: %+v", needs)
	require.NotEmpty(t, needs[1].WaivedAt, "needs of b: %+v", needs)
	w.clean("waived")
}

// tickDone is the done part's plan alone.
func tickDone(s *Snapshot, r TickReq) Plan {
	p, _ := TickDone(s, r)
	return p
}
