package sprint

import (
	"strings"
	"testing"
)

// The dogfood of 2026-09-30 (errata 3 amendment 7): three streams of 100
// with a sentinel every 10, then cards with needs across streams added in
// line, in this order: stream, id, needs, after.
var gateDeps = [][4]string{
	{"a", "a-dep-1", "c-81,b-87", "a-40"},
	{"a", "a-dep-2", "b-7,c-60", "a-97"},
	{"a", "a-dep-3", "b-6,c-11", "a-23"},
	{"a", "a-dep-4", "b-49,c-9", "a-52"},
	{"a", "a-dep-5", "b-1", "a-65"},
	{"b", "b-dep-1", "c-66,a-26", "b-96"},
	{"b", "b-dep-2", "c-97", "b-45"},
	{"b", "b-dep-3", "c-19,a-5", "b-51"},
	{"b", "b-dep-4", "c-85", "b-83"},
	{"b", "b-dep-5", "c-90,a-68", "b-42"},
	{"c", "c-dep-1", "b-84", "c-34"},
	{"c", "c-dep-2", "a-9,a-47", "c-80"},
	{"c", "c-dep-3", "b-60,b-41", "c-62"},
	{"c", "c-dep-4", "b-34", "c-89"},
	{"c", "c-dep-5", "a-37", "c-22"},
}

func gateStreams(t *testing.T, n int) *world {
	t.Helper()
	w := newWorld(t, "reader-a", "reader-b")
	for _, s := range []string{"a", "b", "c"} {
		w.must(Add(w.s, AddReq{Stream: s, Count: n, Every: 10}))
	}
	return w
}

func depAdd(d [4]string) AddReq {
	return AddReq{Stream: d[0], IDs: []string{d[1]}, Needs: Split(d[2]), After: d[3]}
}

// Each add that closes a cycle through the sentinels is refused, naming its
// own loop with the sentinels in it: b-dep-5 through a-gate-6 and b-gate-8,
// and each of c-dep-1, c-dep-2 and c-dep-3 through a loop of its own (each
// checked by hand: the card is before a gate of c and needs a card behind a
// gate of the other stream, which needs a card before it that needs a card
// behind that gate of c); never the loop of another add. The rest are
// admitted, and the table holds no cycle for check to report.
func TestAddRefusesACycleThroughTheGates(t *testing.T) {
	t.Parallel()
	w := gateStreams(t, 100)
	refused := map[string]string{
		"b-dep-5": "b-dep-5 needs a-68 needs a-gate-6 needs a-dep-1 needs b-87 needs b-gate-8 needs b-dep-5 (through sentinel a-gate-6, b-gate-8",
		"c-dep-1": "c-dep-1 needs b-84 needs b-gate-8 needs b-dep-2 needs c-97 needs c-gate-9 needs c-dep-1 (through sentinel b-gate-8, c-gate-9",
		"c-dep-2": "c-dep-2 needs a-47 needs a-gate-4 needs a-dep-1 needs c-81 needs c-gate-8 needs c-dep-2 (through sentinel a-gate-4, c-gate-8",
		"c-dep-3": "c-dep-3 needs b-60 needs b-gate-5 needs b-dep-2 needs c-97 needs c-gate-9 needs c-dep-3 (through sentinel b-gate-5, c-gate-9",
	}
	for _, d := range gateDeps {
		p := Add(w.s, depAdd(d))
		want, ok := refused[d[1]]
		if !ok {
			w.must(p)
			continue
		}
		if len(p.Units) != 0 || len(p.Refused) != 1 || !strings.HasPrefix(p.Refused[0].Why, "the needs would make a cycle: "+want) {
			t.Fatalf("%s: %+v", d[1], p)
		}
		if w.s.Work.Card(d[1]) != nil {
			t.Fatalf("%s was written", d[1])
		}
	}
	for _, id := range []string{"c-dep-4", "c-dep-5"} {
		if w.s.Work.Card(id) == nil {
			t.Fatalf("%s was not admitted", id)
		}
	}
	if cs := Cycles(w.s); len(cs) != 0 {
		t.Fatalf("a cycle on the table: %v", cs)
	}
}

// A need across streams that lands before a sentinel on both sides makes no
// cycle: each card is before the other stream's gate, and nothing behind a
// gate is needed by what is before the other.
func TestACrossNeedBeforeTheGatesIsAdmitted(t *testing.T) {
	t.Parallel()
	w := gateStreams(t, 30)
	w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a-x"}, Needs: []string{"b-5"}, After: "a-5"}))
	w.must(Add(w.s, AddReq{Stream: "b", IDs: []string{"b-x"}, Needs: []string{"a-6"}, After: "b-6"}))
	// Behind a gate on one side only: b-y waits behind b-gate-1 and needs
	// a-7, before a-gate-1, which is ready.
	w.must(Add(w.s, AddReq{Stream: "b", IDs: []string{"b-y"}, Needs: []string{"a-7"}, After: "b-15"}))
	// Before a-gate-1 and needing b-15 behind b-gate-1: b-gate-1 needs b-x,
	// which needs a-6, ready. No cycle.
	w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a-z"}, Needs: []string{"b-15"}, After: "a-3"}))
	// Before b-gate-1 and needing a-25 behind a-gate-2 closes one: a-gate-2
	// needs a-z (before it), which needs b-15, behind b-gate-1, which needs b-z.
	p := Add(w.s, AddReq{Stream: "b", IDs: []string{"b-z"}, Needs: []string{"a-25"}, After: "b-2"})
	want := "the needs would make a cycle: b-z needs a-25 needs a-gate-2 needs a-z needs b-15 needs b-gate-1 needs b-z (through sentinel a-gate-2, b-gate-1"
	if len(p.Units) != 0 || len(p.Refused) != 1 || !strings.HasPrefix(p.Refused[0].Why, want) {
		t.Fatalf("a cycle through both gates: %+v", p)
	}
	if cs := Cycles(w.s); len(cs) != 0 {
		t.Fatalf("a cycle on the table: %v", cs)
	}
}

// A store written before the check walked the gates holds the cycle: check
// names it with the cards it keeps from ever being reached, and an add that
// does not touch it is admitted.
func TestCheckReportsACycleAlreadyOnTheTable(t *testing.T) {
	t.Parallel()
	w := gateStreams(t, 100)
	for _, d := range gateDeps[:9] {
		w.must(Add(w.s, depAdd(d)))
	}
	// b-dep-5 as the build before this one admitted it.
	p := Add(w.s, AddReq{Stream: "b", IDs: []string{"b-dep-5"}, After: "b-42"})
	w.must(p)
	b := w.s.Work.Card("b-dep-5")
	b.Col = Waiting
	b.Fields["needs"] = "c-90,a-68"
	w.s.Work.cells, w.s.Work.lines = nil, nil
	cs := Cycles(w.s)
	if len(cs) != 1 {
		t.Fatalf("cycles: %v", cs)
	}
	got := cs[0].String()
	if !strings.HasPrefix(got, "a cycle through ") || !strings.Contains(got, "a-gate-6") || !strings.Contains(got, "b-gate-8") || !strings.HasSuffix(got, " cards can never be reached") {
		t.Fatalf("cycle: %s", got)
	}
	// Every card behind the two gates and the gates after them, in both
	// streams, and the cards of the loop and what waits on them.
	if cs[0].Stuck < 40 {
		t.Fatalf("stuck: %d (%s)", cs[0].Stuck, got)
	}
	found := false
	for _, v := range Check(w.s, nil) {
		if v.Rule == 11 && v.Detail == got {
			found = true
		}
	}
	if !found {
		t.Fatalf("check does not report it: %v", Check(w.s, nil))
	}
	// An add that reaches the cycle and closes none of its own is admitted:
	// the cycle is check's to report, not this add's.
	w.must(Add(w.s, AddReq{Stream: "d", IDs: []string{"d-1"}, Needs: []string{"b-dep-5", "a-gate-6"}}))
	for _, d := range gateDeps[13:] {
		w.must(Add(w.s, depAdd(d)))
	}
	// The tick raises it as "an invariant is broken".
	tp, _ := TickCheck(w.s, TickReq{})
	raised := false
	for _, u := range tp.Units {
		for _, n := range u.Notes {
			if n.Type == NInvariant && strings.Contains(n.What, "a cycle through") {
				raised = true
			}
		}
	}
	for _, n := range tp.Notes {
		if n.Type == NInvariant && strings.Contains(n.What, "a cycle through") {
			raised = true
		}
	}
	if !raised {
		t.Fatalf("the tick raises no judgment: %+v", tp)
	}
}

// The walk is linear: at 100,000 cards in one stream with a gate every 10,
// the check of an add and check's walk of the whole table each look at each
// card a bounded number of times, not every card before every gate.
func TestAddCycleWalkBound(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a", "reader-b")
	w.must(Add(w.s, AddReq{Stream: "big", Count: 100000, Every: 10}))
	w.must(Add(w.s, AddReq{Stream: "o", Count: 20}))
	cards := len(w.s.Work.Cards())
	g := newNeedGraph(w.s, map[string][]string{"o-x": {"big-99990"}}, []*Card{{ID: "o-x", Row: "o", Col: Waiting, Score: 1000000, Fields: map[string]string{"kind": "primary"}}}, nil)
	if c := g.closes([]string{"o-x"}); c != nil {
		t.Fatalf("a cycle: %v", c)
	}
	if g.steps > 3*cards {
		t.Fatalf("the add's walk looked at %d edges over %d cards", g.steps, cards)
	}
	// A gate at the end needing the new card closes a loop through 10,000
	// gates; the walk and the naming stay linear.
	p := Add(w.s, AddReq{Stream: "big", IDs: []string{"big-x"}, Needs: []string{"big-99995"}, After: "big-3"})
	if len(p.Refused) != 1 || !strings.Contains(p.Refused[0].Why, "big-x needs big-99995 needs big-gate-9999 needs big-x") {
		t.Fatalf("the long loop: %+v", p.Refused)
	}
	all := newNeedGraph(w.s, nil, nil, nil)
	var roots []string
	for _, c := range w.s.Work.Column(Waiting) {
		roots = append(roots, c.ID)
	}
	all.components(roots)
	if all.steps > 3*cards {
		t.Fatalf("the whole walk looked at %d edges over %d cards", all.steps, cards)
	}
}
