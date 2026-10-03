package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// cutWorld is b in s2 needing s1-1 and s1-2, with s1-1 dropped: b waits, with
// one blocked judgment naming s1-1.
func cutWorld(t *testing.T) (*world, Open) {
	w := setup(t, 2)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1", "s1-2"}}))
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "obsolete"}))
	blocked := w.openOn("b")
	require.Len(t, blocked, 1, "blocked: %v", blocked)
	require.Equal(t, NBlocked, blocked[0].Note.Type, "blocked: %v", blocked)
	return w, blocked[0]
}

// The cut of a dropped need removes the edge, records who cut it, when and
// why, closes the blocked judgment it answers, and moves nothing itself: the
// resolve that follows moves the card to ready once nothing else holds it.
func TestNeedsCutMakesABlockedCardReady(t *testing.T) {
	t.Parallel()
	w, blocked := cutWorld(t)
	land(w, "s1-2")
	p := w.must(CutNeeds(w.s, NeedsReq{ID: "b", Cut: []string{"s1-1"}, Reason: "b stands without it", Answers: []string{blocked.Note.ID}, Who: "coordinator"}))
	require.Len(t, p.Units, 1, "cut: %+v", p)
	require.Equal(t, []Open{blocked}, p.Units[0].Closes, "the cut does not close the blocked judgment: %+v", p.Units[0])
	require.Empty(t, w.openOn("b"), "a judgment is still open on b: %v", w.s.Open)
	b := w.s.Work.Card("b")
	require.Equal(t, Waiting, b.Col, "the cut itself moves the card: %s", b.Col)
	require.Equal(t, "s1-2", b.F("needs"), "needs after the cut: %v", b.Fields)
	require.Equal(t, "s1-1", b.F("cut"), "cut: %v", b.Fields)
	require.Equal(t, "coordinator", b.F("cut_by"), "cut_by: %v", b.Fields)
	require.Equal(t, stamp(w.s.Now), b.F("cut_at"), "cut_at: %v", b.Fields)
	require.Equal(t, "b stands without it", b.F("reason"), "reason: %v", b.Fields)
	w.must(Resolve(w.s, ResolveReq{Sel: Sel{IDs: []string{"b"}}}))
	require.Equal(t, Ready, w.state("b"), "b after the cut and a resolve")
	w.clean("cut")
}

// A cut of a live need, of a need the card does not name, of a missing need,
// and on a card that is not waiting is refused whole, nothing planned.
func TestNeedsCutRefusesALiveEdgeAndACardNotWaiting(t *testing.T) {
	t.Parallel()
	w, _ := cutWorld(t)
	for _, c := range []struct {
		req NeedsReq
		why string
	}{
		{NeedsReq{ID: "b", Cut: []string{"s1-2"}, Reason: "r"}, "s1-2 is s1:ready, not dropped: only a need dropped off the table is cut"},
		{NeedsReq{ID: "b", Cut: []string{"s1-1", "s1-2"}, Reason: "r"}, "s1-2 is s1:ready, not dropped"},
		{NeedsReq{ID: "b", Cut: []string{"x"}, Reason: "r"}, "b does not need x (its needs: s1-1,s1-2)"},
		{NeedsReq{ID: "s1-2", Cut: []string{"s1-1"}, Reason: "r"}, "s1-2 is ready, not waiting"},
		{NeedsReq{ID: "b", Cut: []string{"s1-1"}}, "needs --cut wants --reason"},
		{NeedsReq{ID: "b", Cut: []string{"s1-1", "s1-1"}, Reason: "r"}, "s1-1 is named twice"},
	} {
		p := CutNeeds(w.s, c.req)
		require.Empty(t, p.Units, "%+v planned: %+v", c.req, p.Units)
		require.Len(t, p.Refused, 1, "%+v: %+v", c.req, p.Refused)
		require.Contains(t, p.Refused[0].Why, c.why, "%+v", c.req)
	}
	w.s.Work.Put(&Card{ID: "m", Row: "s2", Col: Waiting, Score: 9, Rev: 1, Fields: map[string]string{"needs": "gone"}})
	p := CutNeeds(w.s, NeedsReq{ID: "m", Cut: []string{"gone"}, Reason: "r"})
	require.Len(t, p.Refused, 1, "a missing need: %+v", p)
	require.Contains(t, p.Refused[0].Why, "gone has no record in this epoch", "a missing need")
}

// A cut of one of the dropped needs a blocked judgment names closes it and
// writes the rest as a blocked judgment again: no dropped need goes unsaid.
func TestNeedsCutOfSomeNeedsKeepsTheRestJudged(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1", "s1-2"}}))
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1", "s1-2"}}, Reason: "obsolete"}))
	require.Len(t, w.openOn("b"), 1, "blocked: %v", w.s.Open)
	w.must(CutNeeds(w.s, NeedsReq{ID: "b", Cut: []string{"s1-1"}, Reason: "r", Who: "coordinator"}))
	open := w.openOn("b")
	require.Len(t, open, 1, "after the cut: %v", open)
	require.Equal(t, NBlocked, open[0].Note.Type, "after the cut: %v", open)
	require.Equal(t, []string{"s1-2"}, open[0].Note.Needs, "the judgment again names: %v", open[0].Note.Needs)
	require.Equal(t, "s1-2", w.s.Work.Card("b").F("needs"))
}

// The blocked judgment offers the cut as its third decision, the exact
// command for its card and its dropped needs; a judgment's line and a card's
// timeline line say what the cut did.
func TestNeedsCutIsOfferedAndTold(t *testing.T) {
	t.Parallel()
	w, blocked := cutWorld(t)
	require.Equal(t, []string{"drop", "ack", CutDecision}, blocked.Note.Decisions)
	cmds := NoteCommands(blocked.Note, []string{"b"})
	require.Len(t, cmds, 3, "commands: %+v", cmds)
	require.Equal(t, Command{Decision: CutDecision, Lines: []string{"nova-sprint needs b --cut s1-1 --reason '<why>' --answers " + blocked.Note.ID}}, cmds[2])
	line := Line{Kind: LineMove, Table: Work, Card: "b", From: "s2:waiting", To: "s2:waiting", Actor: "coordinator", Verb: "needs",
		Set: map[string]string{"cut": "s1-1", "cut_by": "coordinator", "needs": "s1-2"}, Text: map[string]string{"reason": "b stands without it"}}
	require.Equal(t, "b: needs on dropped cards cut by coordinator (cut: s1-1; needs now: s1-2)", Render(line))
	require.Equal(t, []string{"reason: b stands without it"}, RenderText(line))
	require.Len(t, Timeline([]Line{line}, "b"), 1, "the cut is no field set in passing")
	_ = w
}
