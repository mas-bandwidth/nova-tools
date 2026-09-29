package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A worker that held generation 1 of a card dealt away and back again sends
// its finish with --as only: the finish names no generation, so it is refused
// and nothing moves; by selection, with no id at all, it is refused too.
func TestProbe1bStaleFinishWithoutGeneration(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(StartStep(sprint.StartReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	first := h.snap().Fleet.Card("s1-1.w1").Row
	h.must(TakeStep(sprint.TakeReq{As: first, Sel: sprint.Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 1}}))
	h.must(FleetStep(sprint.FleetReq{Op: "down", Member: first}))
	second := h.snap().Fleet.Card("s1-1.w1").Row
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: first}))
	h.must(FleetStep(sprint.FleetReq{Op: "down", Member: second}))
	c := h.snap().Fleet.Card("s1-1.w1")
	if c.Row != first || c.Int("gen") != 3 {
		t.Fatalf("the card is at %s gen %d", c.Row, c.Int("gen"))
	}
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
