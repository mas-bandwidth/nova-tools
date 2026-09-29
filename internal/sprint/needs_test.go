package sprint

import (
	"strings"
	"testing"
)

// land drives primaries (all ready) through to landed, one merge step each.
func land(w *world, ids ...string) {
	w.t.Helper()
	for _, id := range ids {
		accepted(w, id)
		w.must(MergeStep(w.s, MergeReq{Stream: w.s.Work.Card(id).Row, Batch: 1}))
		if w.state(id) != Landed {
			w.t.Fatalf("%s is %s", id, w.state(id))
		}
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
	if p := Lawful(Plan{Units: []Unit{move}}); len(p.Units) != 0 || !strings.Contains(p.Refused[0].Why, "carries none") {
		t.Fatalf("a move with no pre-state: %+v", p)
	}
	p := Plan{Units: []Unit{move}}
	p.on(w.s)
	if p = Lawful(p); len(p.Units) != 0 || p.Refused[0].Why != "b needs s1-1, not landed: it waits" {
		t.Fatalf("a move past a need: %+v", p)
	}
	create := Unit{Key: "c", Stream: "s2", Changes: []Change{change(Work, createEntry("c", "s2", Ready, 9, map[string]string{"needs": "s1-1"}))}}
	if p := Lawful(Plan{Units: []Unit{create}}); len(p.Units) != 0 {
		t.Fatalf("admitted ready past a need: %+v", p)
	}
	// Resolve, the landing trigger and ack are the steps that move a primary
	// into ready: none moves b while s1-1 has not landed.
	if p := Resolve(w.s, ResolveReq{Sel: Sel{IDs: []string{"b"}}}); len(p.Units) != 0 {
		t.Fatalf("resolve moved b: %+v", p)
	}
	if u := resolveAfter(w.s, nil, ""); len(u) != 0 {
		t.Fatalf("the trigger moved b: %+v", u)
	}
	// Rule 11: a primary past waiting with a need not landed is a violation.
	b.Col = Ready
	w.s.Work.cells = nil
	if v := Check(w.s, nil); len(v) != 1 || v[0].Rule != 11 || !strings.Contains(v[0].Detail, "b is ready and needs s1-1") {
		t.Fatalf("check: %v", v)
	}
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
			if w.state("c"+itoa(j)) != Waiting {
				t.Fatalf("c%d is %s with c%d not landed", j, w.state("c"+itoa(j)), i)
			}
		}
		land(w, "c"+itoa(i))
	}
	w.must(Add(w.s, AddReq{Stream: "d", IDs: []string{"a"}}))
	w.must(Add(w.s, AddReq{Stream: "d", IDs: []string{"b", "c"}, Needs: []string{"a"}}))
	w.must(Add(w.s, AddReq{Stream: "d", IDs: []string{"d"}, Needs: []string{"b", "c"}}))
	land(w, "a")
	if w.state("b") != Ready || w.state("c") != Ready || w.state("d") != Waiting {
		t.Fatalf("after a: b %s c %s d %s", w.state("b"), w.state("c"), w.state("d"))
	}
	land(w, "b")
	if w.state("d") != Waiting {
		t.Fatalf("d moved with c not landed")
	}
	land(w, "c")
	if w.state("d") != Ready {
		t.Fatalf("d is %s", w.state("d"))
	}
}

// H11: add refuses needs that would make a cycle, naming it; nothing is written.
func TestAddRefusesACycle(t *testing.T) {
	t.Parallel()
	w := setup(t, 0)
	// b needs c needs x (a state no verb makes today; a sentinel admitted
	// before a card can): an x admitted needing b closes a cycle of three.
	w.s.Work.Rows = append(w.s.Work.Rows, "s9")
	w.s.Work.Put(&Card{ID: "c", Row: "s9", Col: Waiting, Score: 1, Rev: 1, Fields: map[string]string{"needs": "x"}})
	w.s.Work.Put(&Card{ID: "b", Row: "s9", Col: Waiting, Score: 2, Rev: 1, Fields: map[string]string{"needs": "c"}})
	p := Add(w.s, AddReq{Stream: "s9", IDs: []string{"x"}, Needs: []string{"b"}})
	if len(p.Units) != 0 || len(p.Refused) != 1 || p.Refused[0].Why != "the needs would make a cycle: x needs b needs c needs x; nothing is written" {
		t.Fatalf("a cycle of three: %+v", p)
	}
	if p := Add(w.s, AddReq{Stream: "s9", IDs: []string{"y", "z"}, Needs: []string{"z"}}); len(p.Units) != 0 || len(p.Refused) != 2 {
		t.Fatalf("a need of itself: %+v", p)
	}
	if c := NeedsCycle(w.s, map[string][]string{"p": {"q"}, "q": {"r"}, "r": {"p"}}); strings.Join(c, ",") != "p,q,r,p" {
		t.Fatalf("NeedsCycle: %v", c)
	}
}

// H11: a dropped need acknowledged by the coordinator is waived, by whom and
// when, and counts as satisfied: the primary moves to ready in the ack.
func TestAWaivedNeedIsSatisfied(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}}))
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "obsolete"}))
	blocked := w.openOn("b")
	if len(blocked) != 1 || blocked[0].Note.Type != NBlocked {
		t.Fatalf("blocked: %v", blocked)
	}
	p := w.must(Ack(w.s, AckReq{Notes: []string{blocked[0].Note.ID}, Reason: "not needed after all", Who: "coordinator"}))
	b := w.s.Work.Card("b")
	if b.Col != Ready || b.F("waived") != "s1-1" || b.F("waived_by") != "coordinator" || b.F("waived_at") != stamp(w.s.Now) {
		t.Fatalf("ack: %s %v (%+v)", b.Col, b.Fields, p.Units)
	}
	if p := Lawful(p); len(p.Refused) != 0 {
		t.Fatalf("the lifecycle refuses the waived move: %+v", p.Refused)
	}
	w.clean("waived")
	needs, _ := NeedsOf(w.s, "b")
	if len(needs) != 1 || !needs[0].Waived || needs[0].State != "off the table (dropped)" {
		t.Fatalf("NeedsOf: %+v", needs)
	}
}
