package sprint

import (
	"strings"
	"testing"
)

// gensOf is the live generation of each named work card: what a worker
// holding the card names in take and finish.
func gensOf(s *Snapshot, ids ...string) map[string]int {
	out := map[string]int{}
	for _, id := range ids {
		if c := s.Fleet.Card(id); c != nil {
			out[id] = c.Int("gen")
		}
	}
	return out
}

// A take by id and every finish name the generation: a card without one is
// refused naming the live generation, and a finish by selection without --as
// is refused outright. Nothing moves on a refusal.
func TestTakeAndFinishNameTheGeneration(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Start(w.s, StartReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	c := w.s.Fleet.Card("s1-1.w1")
	m := c.Row

	take := Take(w.s, TakeReq{As: m, Sel: Sel{IDs: []string{c.ID}}})
	if len(take.Units) != 0 || len(take.Refused) != 1 || !strings.Contains(take.Refused[0].Why, "names no generation; the live one is 1: take s1-1.w1@1") {
		t.Fatalf("a take by id without a generation: %+v", take)
	}
	// a take by selection needs none: it reports the generation it took
	sel := Take(w.s, TakeReq{As: m})
	if len(sel.Units) != 1 || !strings.Contains(sel.Units[0].Moved, "gen=1") {
		t.Fatalf("a take by selection: %+v", sel)
	}
	w.must(Take(w.s, TakeReq{As: m, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))

	for name, r := range map[string]FinishReq{
		"by id":              {As: m, Sel: Sel{IDs: []string{c.ID}}},
		"by id, no --as":     {Sel: Sel{IDs: []string{c.ID}}},
		"by selection --as":  {As: m},
		"by stream --as":     {As: m, Sel: Sel{Stream: "s1"}},
		"by selection alone": {},
	} {
		p := Finish(w.s, r)
		if len(p.Units) != 0 || len(p.Refused) != 1 {
			t.Fatalf("%s: a finish without a generation: %+v", name, p)
		}
		why := p.Refused[0].Why
		if name == "by selection alone" {
			if p.Refused[0].Key != "finish" || !strings.Contains(why, "--as <member>") {
				t.Fatalf("%s: %+v", name, p.Refused)
			}
			continue
		}
		if p.Refused[0].Key != c.ID || !strings.Contains(why, "the live one is 1: finish s1-1.w1@1") {
			t.Fatalf("%s: %+v", name, p.Refused)
		}
	}
	w.must(Finish(w.s, FinishReq{As: m, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	if w.state("s1-1") != Review {
		t.Fatalf("finished at the live generation: %s", w.state("s1-1"))
	}
	w.clean("finished")
}
