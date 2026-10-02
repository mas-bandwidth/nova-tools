package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
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
		assert.Empty(t, res.Moved, "%s: a finish naming no generation moved: %v refused %v", name, res.Moved, res.Refused)
		assert.Len(t, res.Refused, 1, "%s: a finish naming no generation moved: %v refused %v", name, res.Moved, res.Refused)
	}
	got := h.snap().Fleet.Card("s1-1.w1")
	require.Equal(t, sprint.Working, got.Col, "a refused finish changed the card: %s gen %d, primary %s", got.Col, got.Int("gen"), h.state("s1-1"))
	require.Equal(t, 3, got.Int("gen"), "a refused finish changed the card: %s gen %d, primary %s", got.Col, got.Int("gen"), h.state("s1-1"))
	require.Equal(t, sprint.Working, h.state("s1-1"), "a refused finish changed the card: %s gen %d, primary %s", got.Col, got.Int("gen"), h.state("s1-1"))
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
	c := h.snap().Fleet.Card("s1-1.w1")
	require.Equal(t, sprint.Withdrawn, c.Col, "withdrawn: card %s:%s, primary %s", c.Row, c.Col, h.state("s1-1"))
	require.Equal(t, sprint.Ready, h.state("s1-1"), "withdrawn: card %s:%s, primary %s", c.Row, c.Col, h.state("s1-1"))
	h.clean("withdrawn")
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	s := h.snap()
	c, pr := s.Fleet.Card("s1-1.w1"), s.Work.Card("s1-1")
	require.Equal(t, "m2", c.Row, "dealt again: %s:%s gen %d (was %d) score %v (was %v); primary work %s attempt %d", c.Row, c.Col, c.Int("gen"), before.Int("gen"), c.Score, before.Score, pr.F("work"), pr.Int("attempt"))
	require.Equal(t, sprint.Ready, c.Col, "dealt again: %s:%s gen %d (was %d) score %v (was %v); primary work %s attempt %d", c.Row, c.Col, c.Int("gen"), before.Int("gen"), c.Score, before.Score, pr.F("work"), pr.Int("attempt"))
	require.Greater(t, c.Int("gen"), before.Int("gen")+1, "dealt again: %s:%s gen %d (was %d) score %v (was %v); primary work %s attempt %d", c.Row, c.Col, c.Int("gen"), before.Int("gen"), c.Score, before.Score, pr.F("work"), pr.Int("attempt"))
	require.Equal(t, before.Score, c.Score, "dealt again: %s:%s gen %d (was %d) score %v (was %v); primary work %s attempt %d", c.Row, c.Col, c.Int("gen"), before.Int("gen"), c.Score, before.Score, pr.F("work"), pr.Int("attempt"))
	require.Equal(t, c.ID, pr.F("work"), "dealt again: %s:%s gen %d (was %d) score %v (was %v); primary work %s attempt %d", c.Row, c.Col, c.Int("gen"), before.Int("gen"), c.Score, before.Score, pr.F("work"), pr.Int("attempt"))
	require.Equal(t, 1, pr.Int("attempt"), "dealt again: %s:%s gen %d (was %d) score %v (was %v); primary work %s attempt %d", c.Row, c.Col, c.Int("gen"), before.Int("gen"), c.Score, before.Score, pr.F("work"), pr.Int("attempt"))
	require.Nil(t, s.Fleet.Card("s1-1.w2"), "dealt again: %s:%s gen %d (was %d) score %v (was %v); primary work %s attempt %d", c.Row, c.Col, c.Int("gen"), before.Int("gen"), c.Score, before.Score, pr.F("work"), pr.Int("attempt"))
	h.must(TakeStep(sprint.TakeReq{As: "m2", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
	h.must(FinishStep(sprint.FinishReq{As: "m2", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}}))
	h.clean("finished")
}
