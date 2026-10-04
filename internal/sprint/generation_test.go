package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
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
	require.Empty(t, take.Units, "a take by id without a generation: %+v", take)
	require.Len(t, take.Refused, 1, "a take by id without a generation: %+v", take)
	require.Contains(t, take.Refused[0].Why, "names no generation; the live one is 1: take s1-1.w1@1", "a take by id without a generation: %+v", take)
	// a take by selection needs none: it reports the generation it took
	sel := Take(w.s, TakeReq{As: m})
	require.Len(t, sel.Units, 1, "a take by selection: %+v", sel)
	require.Contains(t, sel.Units[0].Moved, "gen=1", "a take by selection: %+v", sel)
	w.must(Take(w.s, TakeReq{As: m, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))

	for name, r := range map[string]FinishReq{
		"by id":              {As: m, Sel: Sel{IDs: []string{c.ID}}},
		"by id, no --as":     {Sel: Sel{IDs: []string{c.ID}}},
		"by selection --as":  {As: m},
		"by stream --as":     {As: m, Sel: Sel{Stream: "s1"}},
		"by selection alone": {},
	} {
		p := Finish(w.s, r)
		require.Empty(t, p.Units, "%s: a finish without a generation: %+v", name, p)
		require.Len(t, p.Refused, 1, "%s: a finish without a generation: %+v", name, p)
		why := p.Refused[0].Why
		if name == "by selection alone" {
			require.Equal(t, "finish", p.Refused[0].Key, "%s: %+v", name, p.Refused)
			require.Contains(t, why, "--as <member>", "%s: %+v", name, p.Refused)
			continue
		}
		require.Equal(t, c.ID, p.Refused[0].Key, "%s: %+v", name, p.Refused)
		require.Contains(t, why, "the live one is 1: finish s1-1.w1@1", "%s: %+v", name, p.Refused)
	}
	w.must(Finish(w.s, FinishReq{As: m, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	require.Equal(t, Review, w.state("s1-1"), "finished at the live generation: %s", w.state("s1-1"))
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
	require.Equal(t, Ready, w.state("s1-1"), "withdrawn: primary %s, card at %s gen %d", w.state("s1-1"), placeWord(gone), genW)
	require.Equal(t, Withdrawn, gone.Col, "withdrawn: primary %s, card at %s gen %d", w.state("s1-1"), placeWord(gone), genW)
	require.Greater(t, genW, gen0, "withdrawn: primary %s, card at %s gen %d", w.state("s1-1"), placeWord(gone), genW)
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
	require.Empty(t, stale.Units, "a finish of the withdrawn generation: %+v", stale)
	require.Len(t, stale.Refused, 1, "a finish of the withdrawn generation: %+v", stale)
	require.Contains(t, stale.Refused[0].Why, "stale", "a finish of the withdrawn generation: %+v", stale)
	w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	reads := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	require.Len(t, reads, 2, "read cards of attempt 1: %v", reads)
	// rework advances the attempt: the next card is w2 and its reads are r2
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "f"}))
	pr = w.s.Work.Card("s1-1")
	require.Equal(t, 2, pr.Int("attempt"), "rework: attempt %d work %s", pr.Int("attempt"), pr.F("work"))
	require.Equal(t, "s1-1.w2", pr.F("work"), "rework: attempt %d work %s", pr.Int("attempt"), pr.F("work"))
	w.clean("reworked")
}
