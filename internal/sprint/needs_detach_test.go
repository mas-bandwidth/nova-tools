package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A dropped need is detached by the next resolve. The card is ready, the
// story names the need and that it was dropped, and a second resolve does
// not tell it again. tla/Needs.tla Drop then Tick.
func TestADroppedNeedDetachesOnTheNextResolve(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}}))
	w.seedDroppedNeed("s1-1")
	w.must(Resolve(w.s, ResolveReq{}))
	require.Equal(t, Ready, w.state("b"), "b is %s", w.state("b"))
	require.Empty(t, w.notesOf(NBlocked), "a dropped need opened a judgment")
	story := w.notesOf("need detached")
	require.Len(t, story, 1, "story: %+v", story)
	require.Contains(t, story[0].What, "s1-1", "story: %+v", story)
	require.Contains(t, story[0].What, "dropped", "story: %+v", story)
	w.must(Resolve(w.s, ResolveReq{}))
	require.Len(t, w.notesOf("need detached"), 1, "told again")
	w.clean("detached")
}

// One need landed and one dropped: both are satisfied, and the landed id
// stays in the field.
func TestALandedNeedAndADroppedNeedLeaveTheCardReady(t *testing.T) {
	t.Parallel()
	w := setup(t, 0)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"x", "y"}, Brief: proBrief}))
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"x", "y"}}))
	land(w, "x")
	require.Equal(t, Waiting, w.state("b"), "b moved while y is live: %s", w.state("b"))
	w.seedDroppedNeed("y")
	w.must(Resolve(w.s, ResolveReq{}))
	b := w.s.Work.Card("b")
	require.Equal(t, Ready, b.Col, "b is %s needs %q", b.Col, b.F("needs"))
	require.Equal(t, "x", b.F("needs"), "b is %s needs %q", b.Col, b.F("needs"))
	story := w.notesOf("need detached")
	require.Len(t, story, 1, "story: %+v", story)
	require.Contains(t, story[0].What, "y", "story: %+v", story)
	require.Contains(t, story[0].What, "dropped", "story: %+v", story)
	require.Empty(t, w.notesOf(NBlocked))
	w.clean("both satisfied")
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

// A need that names no card is detached after one resolve, with that note.
func TestANeedThatNamesNoCardDetachesAfterOneTick(t *testing.T) {
	t.Parallel()
	w := setup(t, 0)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"b"}, Brief: proBrief}))
	c := w.s.Work.Card("b")
	c.Col = Waiting
	c.Fields["needs"] = "typo"
	w.s.Work.Put(c) // the column index is rebuilt; the revision is the card's
	w.must(Resolve(w.s, ResolveReq{}))
	require.Equal(t, Ready, w.state("b"), "b is %s", w.state("b"))
	story := w.notesOf("need detached")
	require.Len(t, story, 1, "story: %+v", story)
	require.Equal(t, "need typo names no card; detached", story[0].What, "story: %+v", story)
	require.Empty(t, w.notesOf(NMissingNeed))
	w.clean("absent")
}

// Drop detaches every waiting dependant in the drop step and prints one
// line each. They stay waiting, and the next resolve makes them ready.
func TestDropDetachesThreeWaitingDependantsAtOnce(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"a", "b", "c"}, Needs: []string{"s1-1"}}))
	p := w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "obsolete"}))
	for _, id := range []string{"a", "b", "c"} {
		var line string
		for _, u := range p.Units {
			if strings.Contains(u.Moved, "DETACHED") && strings.Contains(u.Moved, id) {
				line = u.Moved
			}
		}
		require.Contains(t, line, "DETACHED", "%s: %q", id, line)
		require.Contains(t, line, "dropped", "%s: %q", id, line)
		require.Equal(t, Waiting, w.state(id), "%s is %s", id, w.state(id))
		require.Empty(t, w.s.Work.Card(id).F("needs"), "%s still names s1-1", id)
	}
	w.must(Resolve(w.s, ResolveReq{}))
	for _, id := range []string{"a", "b", "c"} {
		require.Equal(t, Ready, w.state(id), "%s is %s", id, w.state(id))
	}
	require.Empty(t, w.notesOf(NBlocked))
	w.clean("three detached")
}
