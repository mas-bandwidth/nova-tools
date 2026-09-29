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
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
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

// G1: start after a withdrawal deals the same work card again at a new
// generation, with its score and attempt unchanged; the attempt advances only
// on rework, and read cards are numbered by it.
func TestStartAfterAWithdrawalDealsTheSameCard(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1", "s1-2"}}}))
	first := w.s.Fleet.Card("s1-1.w1")
	gen0, score0 := first.Int("gen"), first.Score
	w.must(Take(w.s, TakeReq{As: first.Row, Sel: Sel{IDs: []string{first.ID}}, Gens: gensOf(w.s, first.ID)}))
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m2"}))
	gone := w.s.Fleet.Card("s1-1.w1")
	genW := gone.Int("gen")
	if w.state("s1-1") != Ready || gone.Col != Withdrawn || genW <= gen0 {
		t.Fatalf("withdrawn: primary %s, card at %s gen %d", w.state("s1-1"), placeWord(gone), genW)
	}
	w.clean("withdrawn")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1", "s1-2"}}}))
	pr := w.s.Work.Card("s1-1")
	c := w.s.Fleet.Card("s1-1.w1")
	switch {
	case pr.Col != Working || pr.F("work") != "s1-1.w1" || pr.Int("attempt") != 1:
		t.Fatalf("the primary: %s work %s attempt %d", pr.Col, pr.F("work"), pr.Int("attempt"))
	case c.Col != Ready || c.Int("gen") <= genW || c.F("member") != c.Row:
		t.Fatalf("the card dealt again: %s gen %d member %s", placeWord(c), c.Int("gen"), c.F("member"))
	case c.Score != score0 || c.Score != pr.Score || c.Int("attempt") != 1 || c.F("withdrawn") != "" || c.F("taken") != "":
		t.Fatalf("the card changed: score %v (was %v) attempt %d withdrawn %q taken %q", c.Score, score0, c.Int("attempt"), c.F("withdrawn"), c.F("taken"))
	case w.s.Fleet.Card("s1-1.w2") != nil:
		t.Fatalf("a second attempt's card was cut")
	}
	w.clean("dealt again")
	// the worker that held the old generation is stale; the new one finishes
	stale := Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: gen0}})
	if len(stale.Units) != 0 || len(stale.Refused) != 1 || !strings.Contains(stale.Refused[0].Why, "stale") {
		t.Fatalf("a finish of the withdrawn generation: %+v", stale)
	}
	w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	if reads := readsAt(w.s, w.s.Work.Card("s1-1"), 1); len(reads) != 2 {
		t.Fatalf("read cards of attempt 1: %v", reads)
	}
	// rework advances the attempt: the next card is w2 and its reads are r2
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "f"}))
	if pr := w.s.Work.Card("s1-1"); pr.Int("attempt") != 2 || pr.F("work") != "s1-1.w2" {
		t.Fatalf("rework: attempt %d work %s", pr.Int("attempt"), pr.F("work"))
	}
	w.clean("reworked")
}
