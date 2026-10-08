package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A card never waits on a need that is gone (docs/SPEC-SPRINT.md section 11,
// "A need that is gone"; tla/Needs.tla): the drain detaches a named need whose
// card is dropped, replaced, archived or absent, writes one story line, and
// moves the card to ready when nothing else holds it. The judgment "a primary
// is blocked on something dropped" is retired.

// movedWith is how many units of a plan moved with a line containing sub.
func movedWith(p Plan, sub string) int {
	n := 0
	for _, u := range p.Units {
		if strings.Contains(u.Moved, sub) {
			n++
		}
	}
	return n
}

// A need dropped off the table is detached by the drain: the card's DEPENDS-ON
// line keeps nothing, its story names the need and that it was dropped, and it
// is ready in the same tick.
func TestADroppedNeedDetachesAndTheCardIsReady(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}}))
	require.Equal(t, Waiting, w.state("b"), "b is %s", w.state("b"))
	p := w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "obsolete"}))
	require.Equal(t, 1, movedWith(p, "DETACHED need s1-1"), "the drop detaches at once: %+v", p.Units)
	require.Equal(t, "", w.s.Work.Card("b").F("needs"), "the dropped need is off the card: %q", w.s.Work.Card("b").F("needs"))
	notes := w.notesOf(NNeedDetached)
	require.Len(t, notes, 1, "one story line: %+v", notes)
	require.Contains(t, notes[0].What, "s1-1", "the story names the need: %q", notes[0].What)
	require.Contains(t, notes[0].What, "dropped", "the story says why: %q", notes[0].What)
	w.must(Resolve(w.s, ResolveReq{}))
	require.Equal(t, Ready, w.state("b"), "the next tick readies b: %s", w.state("b"))
	w.clean("detached")
}

// One need landed and one dropped: the dropped one is detached, the DEPENDS-ON
// view shows the remaining need only, and the card is ready in the drain.
func TestALandedAndADroppedNeedDetach(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1", "s1-2"}}))
	land(w, "s1-1")
	p := w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-2"}}, Reason: "obsolete"}))
	require.Equal(t, 1, movedWith(p, "DETACHED need s1-2"), "the drop detaches what it takes: %+v", p.Units)
	require.Equal(t, "s1-1", w.s.Work.Card("b").F("needs"), "the remaining need only: %q", w.s.Work.Card("b").F("needs"))
	needs, _ := NeedsOf(w.s, "b")
	require.Len(t, needs, 1, "the view shows the remaining need only: %+v", needs)
	require.Equal(t, "s1-1", needs[0].ID, "the view shows the remaining need only: %+v", needs)
	w.must(Resolve(w.s, ResolveReq{}))
	require.Equal(t, Ready, w.state("b"), "the drain readies b: %s", w.state("b"))
	w.clean("detached")
}

// A recut re-points a waiting card's need at the twin in the same step: the
// card's need reads the twin, waits on the twin, and the old id is detached
// with one story line.
func TestARecutRePointsTheNeedAtTheTwin(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}}))
	w.must(Recut(w.s, RecutReq{ID: "s1-1", New: "s1-1b", Tier: "heavy"}))
	require.Equal(t, "s1-1b", w.s.Work.Card("b").F("needs"), "the need reads the twin: %q", w.s.Work.Card("b").F("needs"))
	require.Contains(t, WaitsFor(w.s, w.s.Work.Card("b"), nil), "s1-1b", "the card waits on the twin: %v", WaitsFor(w.s, w.s.Work.Card("b"), nil))
	require.Equal(t, Waiting, w.state("b"), "b is %s", w.state("b"))
	notes := w.notesOf(NNeedDetached)
	require.Len(t, notes, 1, "one story line for the re-cut need: %+v", notes)
	require.Contains(t, notes[0].What, "s1-1", "the story names the old id: %q", notes[0].What)
	require.Contains(t, notes[0].What, "s1-1b", "the story names the twin: %q", notes[0].What)
	require.Empty(t, w.notesOf(NBlocked), "no blocked judgment is raised for a re-cut card")
	w.clean("recut")
}

// A need that names no card at all is detached after one tick, noted once.
func TestANeedThatNamesNoCardDetaches(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}}))
	b := w.s.Work.Card("b")
	b.Fields["needs"] = "ghost"
	b.Rev++
	w.s.Work.Put(b)
	w.must(Resolve(w.s, ResolveReq{}))
	require.Equal(t, Ready, w.state("b"), "the drain readies b: %s", w.state("b"))
	require.Equal(t, "", w.s.Work.Card("b").F("needs"), "the absent need is off the card: %q", w.s.Work.Card("b").F("needs"))
	notes := w.notesOf(NNeedDetached)
	require.Len(t, notes, 1, "noted once: %+v", notes)
	require.Contains(t, notes[0].What, "ghost", "the story names the need: %q", notes[0].What)
	require.Contains(t, notes[0].What, "names no card", "the story says why: %q", notes[0].What)
	w.clean("detached")
}

// Dropping a card three waiting cards need detaches it from all three at once,
// one DETACHED line each, and all three are ready at the next tick.
func TestADropDetachesThreeDependantsAtOnce(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	for _, id := range []string{"b", "c", "d"} {
		w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{id}, Needs: []string{"s1-1"}}))
	}
	p := w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "obsolete"}))
	require.Equal(t, 3, movedWith(p, "DETACHED need s1-1"), "three DETACHED lines: %+v", p.Units)
	require.Len(t, w.notesOf(NNeedDetached), 3, "one story line per card: %+v", w.notesOf(NNeedDetached))
	w.must(Resolve(w.s, ResolveReq{}))
	for _, id := range []string{"b", "c", "d"} {
		assert.Equal(t, Ready, w.state(id), "%s is %s", id, w.state(id))
		assert.Equal(t, "", w.s.Work.Card(id).F("needs"), "%s keeps its detached need: %q", id, w.s.Work.Card(id).F("needs"))
	}
	w.clean("detached")
}
