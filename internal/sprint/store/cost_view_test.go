package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Cost visibility (docs/SPEC-SPRINT.md section 1; sprint/cost_view.go; the owner,
// 2026-10-04): the tick's where record counts every card by its brief's tier and carries,
// per stream, its tiers, its dollars per landed card and its spend by the tier each attempt
// ran on, from the cards the tick reads; where reads no card for them.

func TestTheWhereRecordCountsTiersAndCostsByTier(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("pro-a", "pro"), route("heavy-a", "heavy"))
	h.must(SetStep(sprint.SetReq{Attempts: "6", Who: h.st.Actor}))
	// three tiers across two streams: s1 a flash card and a pro card, s2 a heavy card
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"s1-f"}, Brief: briefOf("flash", "")}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"s1-p"}, Brief: briefOf("pro", "")}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"s2-h"}, Brief: briefOf("heavy", "")}))
	require.Equal(t, map[string]int{"flash": 1, "pro": 1, "heavy": 1}, sprint.TierCounts(h.snap()))

	// s1-f runs flash twice (a dollar each, failed), then pro once (two dollars, ok), and lands
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-f"}}}))
	fail := func(usd string) {
		h.t.Helper()
		wc := h.snap().Fleet.Card(h.snap().Work.Card("s1-f").F("work"))
		g := map[string]int{wc.ID: wc.Int("gen")}
		h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: g}))
		h.must(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: g, Failed: true, Report: "red in attempt " + wc.F("attempt"), Usage: "input=1 actual_usd=" + usd + " actual_by=harness"}))
	}
	fail("1")
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-f"}}, Who: "tester"}))
	fail("1")
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-f"}}, Tier: "pro", Who: "tester"}))
	wc := h.snap().Fleet.Card(h.snap().Work.Card("s1-f").F("work"))
	require.Equal(t, "pro", wc.F(sprint.FieldTier), "attempt 3 runs on pro")
	g := map[string]int{wc.ID: wc.Int("gen")}
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: g}))
	h.must(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: g, Head: "h3", Usage: "input=1 actual_usd=2 actual_by=harness"}))
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-f"}}}))
	h.readAllOK("s1-f")
	h.must(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"s1-f"}}}))
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 10}))
	require.Equal(t, sprint.Landed, h.state("s1-f"))

	costs := sprint.StreamTierCosts(h.snap())
	s1 := costs["s1"]
	assert.Equal(t, map[string]int{"flash": 1, "pro": 1}, s1.Tiers)
	// the card's takes cost four dollars and its reads ran on no route that prices them: it
	// is not priced whole, so its cost per landed card is unknown, never the takes' alone
	assert.Equal(t, sprint.CostUnknown, s1.PerLanded, "one landed card, its reads unpriced")
	assert.Equal(t, 1, s1.Landed)
	assert.Zero(t, s1.LandedPriced)
	assert.Equal(t, "$4.00", s1.TotalCost, "the priced spend, a floor")
	assert.Equal(t, sprint.CostUnknown, s1.SpendPerLanded)
	assert.Equal(t, map[string]string{"flash": "$2.00", "pro": "$2.00"}, s1.CostByTier, "the spend split by the tier each attempt ran on, not the card's ceiling")
	s2 := costs["s2"]
	assert.Equal(t, map[string]int{"heavy": 1}, s2.Tiers)
	assert.Equal(t, "-", s2.PerLanded, "nothing landed")
	assert.Empty(t, s2.CostByTier, "nothing spent")

	// the where record carries them, counted by the tick
	rec := whereOf(h.snap(), h.machineRecord(), h.st.now())
	assert.Equal(t, map[string]int{"flash": 1, "pro": 1, "heavy": 1}, rec.Tiers)
	assert.Equal(t, s1, rec.Streams["s1"])
	assert.Equal(t, "-", sprint.PerLandedOf("-", 0))
	assert.Equal(t, "$0.34", sprint.PerLandedOf("$1.00", 3), "a cent rounded up")
	h.clean("tiers and costs counted")
}

// A card dropped, or re-cut as its twin, keeps its spend in its stream (docs/SPEC-SPRINT.md
// section 1, cost visibility): the drop writes it on the stream's control card in the same
// step, so the stream's spend per landed card cannot improve by taking a costly card off the
// table.
func TestADroppedOrRecutCardsSpendStaysInItsStream(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("pro-a", "pro"))
	h.must(SetStep(sprint.SetReq{Attempts: "6", Who: h.st.Actor}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"s1-a", "s1-b"}, Brief: briefOf("flash", "")}))
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-a", "s1-b"}}}))
	fail := func(id, usd string) {
		h.t.Helper()
		wc := h.snap().Fleet.Card(h.snap().Work.Card(id).F("work"))
		g := map[string]int{wc.ID: wc.Int("gen")}
		h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: g}))
		h.must(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: g, Failed: true, Report: "red", Usage: "input=1 actual_usd=" + usd + " actual_by=harness"}))
	}
	fail("s1-a", "3")
	fail("s1-b", "5")
	before := sprint.StreamTierCosts(h.snap())["s1"]
	require.Equal(t, "$8.00", before.TotalCost)

	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-b"}}, Reason: "obsolete"}))
	h.must(RecutStep(sprint.RecutReq{ID: "s1-a", Tier: "pro", Who: "tester"}))
	require.Nil(t, h.snap().Work.Placed("s1-a"), "the re-cut card is off the table")
	require.Nil(t, h.snap().Work.Placed("s1-b"), "the dropped card is off the table")
	after := sprint.StreamTierCosts(h.snap())["s1"]
	assert.Equal(t, "$8.00", after.TotalCost, "both cards' spend is still the stream's")
	assert.Equal(t, before.Coverage, after.Coverage)
	assert.Equal(t, sprint.DroppedSpend{Cards: 2, Cost: "8", Coverage: sprint.Coverage{Records: 2, Actual: 2}}, after.Dropped)
	assert.Equal(t, 2, h.snap().StreamCtl("s1").Int("dropped"))
	h.clean("dropped spend kept")
}
