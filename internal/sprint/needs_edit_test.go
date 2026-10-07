package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The verb needs (the owner, 2026-10-07: "If it's just dependencies, please check if the
// dependencies are still correct"): a card's needs edited in place, its id, stream, score
// and brief kept, one happened note on its timeline with the actor and the reason, and the
// card moved waiting -> ready in the same step when nothing unlanded is left to wait for.

// Dropping a card's last unlanded need moves it to ready in the same step, as the step that
// lands that need would; dropping one of two leaves it waiting on the other.
func TestNeedsDropMovesACardReadyWhenNothingUnlandedIsLeft(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1", "s1-2"}}))
	require.Equal(t, Waiting, w.state("b"))
	w.seedDroppedNeed("s1-2") // the stale need: dropped off the table, deferred elsewhere

	p := w.must(EditNeeds(w.s, NeedsReq{ID: "b", Drop: []string{"s1-2"}, Reason: "deferred to the next release", Who: "coordinator"}))
	require.Len(t, p.Units, 1, "%+v", p)
	assert.Equal(t, "b needs s1-1,s1-2 -> s1-1: deferred to the next release", p.Units[0].Moved)
	b := w.s.Work.Card("b")
	assert.Equal(t, Waiting, b.Col, "a live need is left: it waits")
	assert.Equal(t, "s1-1", b.F("needs"))
	assert.Equal(t, "s1-1,s1-2 -> s1-1 "+stamp(w.s.Now)+" by coordinator", b.F(FieldNeedsSet))
	notes := w.notesOf(NNeedsSet)
	require.Len(t, notes, 1)
	assert.Equal(t, "coordinator", notes[0].Who, "the actor is on the note")
	assert.Equal(t, []string{"b"}, notes[0].Primaries, "the note is on the card's timeline")
	w.clean("one need dropped")

	p = w.must(EditNeeds(w.s, NeedsReq{ID: "b", Drop: []string{"s1-1"}, Reason: "done elsewhere", Who: "coordinator"}))
	require.Len(t, p.Units, 1, "%+v", p)
	assert.Equal(t, "b needs s1-1 -> -: done elsewhere; b waiting -> ready (its needs landed)", p.Units[0].Moved)
	b = w.s.Work.Card("b")
	assert.Equal(t, Ready, b.Col, "nothing unlanded is left: ready in the same step")
	assert.False(t, b.Has("needs"), "no needs left: the field is unset, not empty")
	assert.NotEmpty(t, b.F(FieldReadyAt), "the ready stamp, as a resolve writes it")
	w.clean("every need dropped")
}

// A drop that leaves the card with landed needs only moves it to ready too: the lifecycle
// judges the move on the needs the entry writes, never on the stale ones it takes off.
func TestNeedsDropToLandedNeedsOnlyIsLawful(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1", "s1-2"}}))
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"c"}, Needs: []string{"s1-2"}}))
	land(w, "s1-1")
	require.Equal(t, Waiting, w.state("b"), "s1-2 is not landed")
	w.seedDroppedNeed("s1-2")
	p := w.must(EditNeeds(w.s, NeedsReq{ID: "b", Drop: []string{"s1-2"}, Reason: "deferred", Who: "coordinator"}))
	require.Len(t, p.Units, 1, "%+v", p)
	assert.Equal(t, Ready, w.state("b"))
	assert.Equal(t, "s1-1", w.s.Work.Card("b").F("needs"), "the landed need stays named")
	w.clean("dropped to landed needs")

	// the lifecycle still refuses a move whose entry writes an unlanded need
	c := w.s.Work.Card("c")
	move := Unit{Key: "c", Stream: "s2", Changes: []Change{change(Work, moveEntry(c, "s2", Ready, map[string]string{"needs": "s1-2"}))}}
	q := Plan{Units: []Unit{move}}
	q.on(w.s)
	q = Lawful(q)
	require.Empty(t, q.Units, "a move past a need the entry writes: %+v", q)
	require.Equal(t, "c needs s1-2, not landed: it waits", q.Refused[0].Why)
}

// A held card whose needs are all dropped stays waiting: release lifts the hold.
func TestNeedsDropLeavesAHeldCardWaiting(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}, Held: true}))
	w.seedDroppedNeed("s1-1")
	w.must(EditNeeds(w.s, NeedsReq{ID: "b", Drop: []string{"s1-1"}, Reason: "deferred", Who: "coordinator"}))
	b := w.s.Work.Card("b")
	assert.Equal(t, Waiting, b.Col, "held: it waits for release")
	assert.False(t, b.Has("needs"))
	assert.True(t, IsHeld(b))
	w.clean("held")
}

// An add that would close a cycle is refused naming the loop, and nothing is written; so is
// a need that is no card on the table, the card itself, a need it has already, a need to
// drop it does not have, and an edit with no reason or by another actor.
func TestNeedsAddRefusesACycleAndBadNeeds(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}}))
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"c"}, Needs: []string{"b"}}))
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"d"}, Needs: []string{"c"}}))
	w.seedDroppedNeed("s1-1")
	before := w.s.Work.Card("b").F("needs")
	for _, tc := range []struct {
		name string
		req  NeedsReq
		want string
	}{
		{"a cycle", NeedsReq{ID: "b", Add: []string{"d"}, Reason: "r", Who: "coordinator"}, "the needs would make a cycle: b needs d needs c needs b; nothing was changed"},
		{"itself", NeedsReq{ID: "b", Add: []string{"b"}, Reason: "r", Who: "coordinator"}, "a card does not need itself; nothing was changed"},
		{"already", NeedsReq{ID: "b", Add: []string{"s1-1"}, Reason: "r", Who: "coordinator"}, "b needs s1-1 already; nothing was changed"},
		{"no card", NeedsReq{ID: "b", Add: []string{"nosuch", "s1-1x"}, Reason: "r", Who: "coordinator"}, "not a card on the table: nosuch (no card), s1-1x (no card); nothing was changed"},
		{"off the table", NeedsReq{ID: "c", Add: []string{"s1-1"}, Reason: "r", Who: "coordinator"}, "not a card on the table: s1-1 (off the table, dropped); nothing was changed"},
		{"not its need", NeedsReq{ID: "b", Drop: []string{"c"}, Reason: "r", Who: "coordinator"}, "c is no need of b (its needs: s1-1); nothing was changed"},
		{"twice", NeedsReq{ID: "b", Drop: []string{"s1-1", "s1-1"}, Reason: "r", Who: "coordinator"}, "s1-1 is named twice; nothing was changed"},
		{"nothing to do", NeedsReq{ID: "b", Reason: "r", Who: "coordinator"}, "needs wants --drop <id>... or --add <id>...: the needs to take off the card and to put on it; nothing was changed"},
		{"no reason", NeedsReq{ID: "b", Drop: []string{"s1-1"}, Who: "coordinator"}, "needs wants --reason <text>: why the needs change, recorded on the card's timeline; nothing was changed"},
		{"another actor", NeedsReq{ID: "b", Drop: []string{"s1-1"}, Reason: "r", Who: "intruder"}, "needs is the coordinator's alone: coordinator, not intruder; nothing was changed"},
		{"no card to edit", NeedsReq{ID: "nosuch", Drop: []string{"s1-1"}, Reason: "r", Who: "coordinator"}, "nosuch is no card on the table (no card, on the table or off it); nothing was changed; run: nova-sprint card nosuch"},
		{"a dropped card to edit", NeedsReq{ID: "s1-1", Drop: []string{"b"}, Reason: "r", Who: "coordinator"}, "s1-1 is no card on the table (kept, dropped); nothing was changed; run: nova-sprint card s1-1"},
	} {
		p := EditNeeds(w.s, tc.req)
		require.Empty(t, p.Units, "%s: %+v", tc.name, p)
		require.Len(t, p.Refused, 1, "%s: %+v", tc.name, p)
		assert.Equal(t, tc.want, p.Refused[0].Why, tc.name)
	}
	assert.Equal(t, before, w.s.Work.Card("b").F("needs"), "a refusal changed the needs")
	w.clean("refused")
}

// A landed card keeps its needs (landed is final); a sentinel's change with sentinel set; a
// ready card takes no need that has not landed, and takes one that has.
func TestNeedsRefusesALandedCardAndASentinelAndAReadyCardGivenAnUnlandedNeed(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-stop"}, Sentinel: true}))
	land(w, "s1-1")
	p := EditNeeds(w.s, NeedsReq{ID: "s1-1", Add: []string{"s1-2"}, Reason: "r", Who: "coordinator"})
	require.Len(t, p.Refused, 1, "%+v", p)
	assert.Equal(t, "s1-1 landed: landed is final, and what needed it went on; nothing was changed", p.Refused[0].Why)
	p = EditNeeds(w.s, NeedsReq{ID: "s1-stop", Add: []string{"s1-2"}, Reason: "r", Who: "coordinator"})
	require.Len(t, p.Refused, 1, "%+v", p)
	assert.Equal(t, "s1-stop is a sentinel: its needs change with nova-sprint sentinel set s1-stop --needs <a,b>; nothing was changed", p.Refused[0].Why)
	p = EditNeeds(w.s, NeedsReq{ID: "s1-2", Add: []string{"s1-3"}, Reason: "r", Who: "coordinator"})
	require.Len(t, p.Refused, 1, "%+v", p)
	assert.True(t, strings.HasPrefix(p.Refused[0].Why, "s1-2 is ready and s1-3 is ready, not landed: a ready card would be dealt before its need"), p.Refused[0].Why)
	// a landed need is taken by a ready card: it records the dependency and moves nothing
	p = w.must(EditNeeds(w.s, NeedsReq{ID: "s1-2", Add: []string{"s1-1"}, Reason: "records what it built on", Who: "coordinator"}))
	require.Len(t, p.Units, 1, "%+v", p)
	assert.Equal(t, Ready, w.state("s1-2"))
	assert.Equal(t, "s1-1", w.s.Work.Card("s1-2").F("needs"))
	w.clean("a ready card given a landed need")
}

// The blocked judgment a dropped need opened is answered by the drop of that need from
// the card, in the same step; one that names a need the card keeps stays open.
func TestNeedsDropAnswersTheBlockedJudgmentOnTheDroppedNeed(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1", "s1-2"}}))
	w.seedDroppedNeed("s1-1")
	w.must(Resolve(w.s, ResolveReq{}))
	open := w.openOn("b")
	require.Len(t, open, 1, "blocked: %v", open)
	require.Equal(t, NBlocked, open[0].Note.Type)
	require.Equal(t, []string{"s1-1"}, open[0].Note.Needs, "the judgment names the dropped need")

	p := w.must(EditNeeds(w.s, NeedsReq{ID: "b", Drop: []string{"s1-1"}, Reason: "done on the base", Who: "coordinator"}))
	require.Len(t, p.Units, 1, "%+v", p)
	assert.Len(t, p.Units[0].Closes, 1, "the blocked judgment is answered: %+v", p.Units[0])
	assert.Empty(t, w.openOn("b"), "answered")
	assert.Equal(t, Waiting, w.state("b"), "s1-2 is still to land")
	assert.Equal(t, "s1-2", w.s.Work.Card("b").F("needs"))
	w.clean("answered")
}
