package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A need that names no card is detached by the resolve that sees it. The
// story line is written once, the id leaves the field, and the card stays
// waiting: the lifecycle judges waiting -> ready from the pre-state, so the
// move is the next resolve. tla/Needs.tla Tick.
func TestANeedThatNamesNoCardDetachesAfterOneTick(t *testing.T) {
	t.Parallel()
	w := setup(t, 0)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"b"}, Brief: proBrief}))
	c := w.s.Work.Card("b")
	c.Col = Waiting
	c.Fields["needs"] = "typo"
	w.s.Work.Put(c)
	w.must(Resolve(w.s, ResolveReq{}))
	require.Equal(t, Waiting, w.state("b"), "b is %s", w.state("b"))
	require.Empty(t, w.s.Work.Card("b").F("needs"), "the name stayed in the field")
	story := w.notesOf("need detached")
	require.Len(t, story, 1, "story: %+v", story)
	require.Equal(t, "need typo names no card; detached", story[0].What, "story: %+v", story)
	require.Empty(t, w.notesOf(NMissingNeed))
	w.must(Resolve(w.s, ResolveReq{}))
	require.Equal(t, Ready, w.state("b"), "b is %s", w.state("b"))
	require.Len(t, w.notesOf("need detached"), 1, "told again")
	w.clean("absent")
}

// A name that is no card leaves the field. A need that names a card stays,
// and the card keeps waiting on it.
func TestANeedThatNamesNoCardLeavesALiveNeed(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}, Brief: proBrief}))
	c := w.s.Work.Card("b")
	c.Fields["needs"] = "s1-1,typo"
	w.s.Work.Put(c)
	w.must(Resolve(w.s, ResolveReq{}))
	b := w.s.Work.Card("b")
	require.Equal(t, Waiting, b.Col, "b is %s", b.Col)
	require.Equal(t, "s1-1", b.F("needs"), "needs %q", b.F("needs"))
	story := w.notesOf("need detached")
	require.Len(t, story, 1, "story: %+v", story)
	require.Equal(t, "need typo names no card; detached", story[0].What, "story: %+v", story)
	w.must(Resolve(w.s, ResolveReq{}))
	require.Equal(t, Waiting, w.state("b"), "b moved while s1-1 is live: %s", w.state("b"))
	require.Len(t, w.notesOf("need detached"), 1, "told again")
	w.clean("live need kept")
}

// Recut re-points the need onto the twin at once. The card waits on the twin.
func TestARecutNeedWaitsOnTheTwin(t *testing.T) {
	t.Parallel()
	w := setup(t, 0)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"x"}, Brief: proBrief, Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"x"}, Who: "coordinator"}))
	w.must(Recut(w.s, RecutReq{ID: "x", Tier: "heavy", Who: "coordinator"}))
	require.Equal(t, "xb", w.s.Work.Card("b").F("needs"), "the need was not re-pointed")
	require.Equal(t, Waiting, w.state("b"), "b is %s", w.state("b"))
	require.Equal(t, "replaced by xb", w.s.Work.Card("x").F("reason"))
	require.Empty(t, w.notesOf(NBlocked))
	w.clean("recut")
}
