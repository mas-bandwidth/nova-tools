package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// A worker that held generation 1 of a card dealt away and back again sends
// its finish with --as only: the finish names no generation, so it is refused
// and nothing moves; by selection, with no id at all, it is refused too.
func TestProbe1bStaleFinishWithoutGeneration(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	first := h.snap().Fleet.Card("s1-1.w1").Row
	h.must(TakeStep(sprint.TakeReq{As: first, Sel: sprint.Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 1}}))
	h.must(FleetStep(sprint.FleetReq{Op: "down", Member: first}))
	second := h.snap().Fleet.Card("s1-1.w1").Row
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: first}))
	h.must(FleetStep(sprint.FleetReq{Op: "down", Member: second}))
	c := h.snap().Fleet.Card("s1-1.w1")
	require.Equal(t, first, c.Row, "the card is at %s gen %d", c.Row, c.Int("gen"))
	require.Equal(t, 3, c.Int("gen"), "the card is at %s gen %d", c.Row, c.Int("gen"))
	h.must(TakeStep(sprint.TakeReq{As: first, Sel: sprint.Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": c.Int("gen")}}))
	for name, r := range map[string]sprint.FinishReq{
		"--as only":           {As: first, Sel: sprint.Sel{IDs: []string{"s1-1.w1"}}, Failed: true, Report: "stale"},
		"by selection, --as":  {As: first, Failed: true, Report: "stale"},
		"by selection, alone": {Failed: true, Report: "stale"},
	} {
		res := h.run(FinishStep(r))
		if len(res.Moved) != 0 || len(res.Refused) != 1 {
			t.Errorf("%s: a finish naming no generation moved: %v refused %v", name, res.Moved, res.Refused)
		}
	}
	if got := h.snap().Fleet.Card("s1-1.w1"); got.Col != sprint.Working || got.Int("gen") != 3 || h.state("s1-1") != sprint.Working {
		t.Fatalf("a refused finish changed the card: %s gen %d, primary %s", got.Col, got.Int("gen"), h.state("s1-1"))
	}
	h.must(FinishStep(sprint.FinishReq{As: first, Sel: sprint.Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 3}}))
	h.clean("finished at the live generation")
}

// G1 through the store: a withdrawn work card waits in the fleet's hidden
// withdrawn column, and start deals that same card again at a new generation.
func TestStartAfterAWithdrawalDealsTheSameCardThroughTheStore(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	before := h.snap().Fleet.Card("s1-1.w1")
	h.must(FleetStep(sprint.FleetReq{Op: "down", Member: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "down", Member: "m2"}))
	if c := h.snap().Fleet.Card("s1-1.w1"); c.Col != sprint.Withdrawn || h.state("s1-1") != sprint.Ready {
		t.Fatalf("withdrawn: card %s:%s, primary %s", c.Row, c.Col, h.state("s1-1"))
	}
	h.clean("withdrawn")
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	s := h.snap()
	c, pr := s.Fleet.Card("s1-1.w1"), s.Work.Card("s1-1")
	if c.Row != "m2" || c.Col != sprint.Ready || c.Int("gen") <= before.Int("gen")+1 || c.Score != before.Score || pr.F("work") != c.ID || pr.Int("attempt") != 1 || s.Fleet.Card("s1-1.w2") != nil {
		t.Fatalf("dealt again: %s:%s gen %d (was %d) score %v (was %v); primary work %s attempt %d", c.Row, c.Col, c.Int("gen"), before.Int("gen"), c.Score, before.Score, pr.F("work"), pr.Int("attempt"))
	}
	h.must(TakeStep(sprint.TakeReq{As: "m2", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
	h.must(FinishStep(sprint.FinishReq{As: "m2", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
	h.clean("finished")
}
