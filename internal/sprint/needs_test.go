package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// land drives primaries (all ready) through to landed, one merge step each.
func land(w *world, ids ...string) {
	w.t.Helper()
	for _, id := range ids {
		accepted(w, id)
		w.must(MergeStep(w.s, MergeReq{Stream: w.s.Work.Card(id).Row, Batch: 1}))
		require.Equal(w.t, Landed, w.state(id), "%s is %s", id, w.state(id))
		w.clean("landed " + id)
	}
}

// H11: no step puts a primary with a need not landed into ready: a plan that
// tries is refused by the lifecycle, and one that carries no pre-state moves
// no primary into ready at all.
func TestNoPlanMovesAPrimaryPastItsNeeds(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}}))
	b := w.s.Work.Card("b")
	move := Unit{Key: "b", Stream: "s2", Changes: []Change{change(Work, moveEntry(b, "s2", Ready, nil))}}
	p := Lawful(Plan{Units: []Unit{move}})
	require.Empty(t, p.Units, "a move with no pre-state: %+v", p)
	require.Contains(t, p.Refused[0].Why, "carries none", "a move with no pre-state: %+v", p)
	p = Plan{Units: []Unit{move}}
	p.on(w.s)
	p = Lawful(p)
	require.Empty(t, p.Units, "a move past a need: %+v", p)
	require.Equal(t, "b needs s1-1, not landed: it waits", p.Refused[0].Why, "a move past a need: %+v", p)
	create := Unit{Key: "c", Stream: "s2", Changes: []Change{change(Work, createEntry("c", "s2", Ready, 9, map[string]string{"needs": "s1-1"}))}}
	p = Lawful(Plan{Units: []Unit{create}})
	require.Empty(t, p.Units, "admitted ready past a need: %+v", p)
	// Resolve, the landing trigger and ack are the steps that move a primary
	// into ready: none moves b while s1-1 has not landed.
	p = Resolve(w.s, ResolveReq{Sel: Sel{IDs: []string{"b"}}})
	require.Empty(t, p.Units, "resolve moved b: %+v", p)
	u := resolveAfter(w.s, nil, "")
	require.Empty(t, u, "the trigger moved b: %+v", u)
	// Rule 11: a primary past waiting with a need not landed is a violation.
	b.Col = Ready
	w.s.Work.cells = nil
	v := Check(w.s, nil)
	require.Len(t, v, 1, "check: %v", v)
	require.Equal(t, 11, v[0].Rule, "check: %v", v)
	require.Contains(t, v[0].Detail, "b is ready and needs s1-1", "check: %v", v)
}

// H11: a chain of five and a diamond resolve one layer at a time, each by the
// step that lands the last need; the invariant holds throughout.
func TestAChainAndADiamondResolveInOrder(t *testing.T) {
	t.Parallel()
	w := setup(t, 0)
	w.must(Add(w.s, AddReq{Stream: "c", IDs: []string{"c1"}}))
	for i := 2; i <= 5; i++ {
		w.must(Add(w.s, AddReq{Stream: "c", IDs: []string{"c" + itoa(i)}, Needs: []string{"c" + itoa(i-1)}}))
	}
	for i := 1; i <= 5; i++ {
		for j := i + 1; j <= 5; j++ {
			require.Equal(t, Waiting, w.state("c"+itoa(j)), "c%d is %s with c%d not landed", j, w.state("c"+itoa(j)), i)
		}
		land(w, "c"+itoa(i))
	}
	w.must(Add(w.s, AddReq{Stream: "d", IDs: []string{"a"}}))
	w.must(Add(w.s, AddReq{Stream: "d", IDs: []string{"b", "c"}, Needs: []string{"a"}}))
	w.must(Add(w.s, AddReq{Stream: "d", IDs: []string{"d"}, Needs: []string{"b", "c"}}))
	land(w, "a")
	require.Equal(t, Ready, w.state("b"), "after a: b %s c %s d %s", w.state("b"), w.state("c"), w.state("d"))
	require.Equal(t, Ready, w.state("c"), "after a: b %s c %s d %s", w.state("b"), w.state("c"), w.state("d"))
	require.Equal(t, Waiting, w.state("d"), "after a: b %s c %s d %s", w.state("b"), w.state("c"), w.state("d"))
	land(w, "b")
	require.Equal(t, Waiting, w.state("d"), "d moved with c not landed")
	land(w, "c")
	require.Equal(t, Ready, w.state("d"), "d is %s", w.state("d"))
}

// H11: add refuses needs that would make a cycle, naming it; nothing is written.
func TestAddRefusesACycle(t *testing.T) {
	t.Parallel()
	w := setup(t, 0)
	// b needs c needs x (a state no verb makes today; a sentinel admitted
	// before a card can): an x admitted needing b closes a cycle of three.
	w.s.Work.SetRows(append(w.s.Work.Rows(), "s9"))
	w.s.Work.Put(&Card{ID: "c", Row: "s9", Col: Waiting, Score: 1, Rev: 1, Fields: map[string]string{"needs": "x"}})
	w.s.Work.Put(&Card{ID: "b", Row: "s9", Col: Waiting, Score: 2, Rev: 1, Fields: map[string]string{"needs": "c"}})
	p := Add(w.s, AddReq{Stream: "s9", IDs: []string{"x"}, Needs: []string{"b"}})
	require.Empty(t, p.Units, "a cycle of three: %+v", p)
	require.Len(t, p.Refused, 1, "a cycle of three: %+v", p)
	require.Equal(t, "the needs would make a cycle: x needs b needs c needs x; nothing is written", p.Refused[0].Why, "a cycle of three: %+v", p)
	p = Add(w.s, AddReq{Stream: "s9", IDs: []string{"y", "z"}, Needs: []string{"z"}})
	require.Empty(t, p.Units, "a need of itself: %+v", p)
	require.Len(t, p.Refused, 2, "a need of itself: %+v", p)
}

// H11: a dropped need acknowledged by the coordinator is waived, by whom and
// when, and counts as satisfied: the primary moves to ready in the ack.
func TestAWaivedNeedIsSatisfied(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}}))
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "obsolete"}))
	blocked := w.openOn("b")
	require.Len(t, blocked, 1, "blocked: %v", blocked)
	require.Equal(t, NBlocked, blocked[0].Note.Type, "blocked: %v", blocked)
	p := w.must(Ack(w.s, AckReq{Notes: []string{blocked[0].Note.ID}, Reason: "not needed after all", Who: "coordinator"}))
	b := w.s.Work.Card("b")
	require.Equal(t, Ready, b.Col, "ack: %s %v (%+v)", b.Col, b.Fields, p.Units)
	require.Equal(t, "s1-1", b.F("waived"), "ack: %s %v (%+v)", b.Col, b.Fields, p.Units)
	require.Equal(t, "coordinator", b.F("waived_by"), "ack: %s %v (%+v)", b.Col, b.Fields, p.Units)
	require.Equal(t, stamp(w.s.Now), b.F("waived_at"), "ack: %s %v (%+v)", b.Col, b.Fields, p.Units)
	lawful := Lawful(p)
	require.Empty(t, lawful.Refused, "the lifecycle refuses the waived move: %+v", lawful.Refused)
	w.clean("waived")
	needs, _ := NeedsOf(w.s, "b")
	require.Len(t, needs, 1, "NeedsOf: %+v", needs)
	require.True(t, needs[0].Waived, "NeedsOf: %+v", needs)
	require.Equal(t, "off the table (dropped)", needs[0].State, "NeedsOf: %+v", needs)
}
