package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// A run that ends with no result is priced at that end, or the record carries why
// it is not (docs/SPEC-SPRINT.md, "Every run's cost, whatever its end"). The world
// is the in-memory twin: no socket, and the clock is the world's.

// noResultCost deals one pro card, takes it, and finishes the take as no result
// with the usage line the member would have reported. It returns that take's
// priced record and the stream's cost view.
func noResultCost(t *testing.T, route Route, usage string) (cardcost.Usage, TierCosts) {
	t.Helper()
	w := setup(t, 1)
	w.s.Routes = []Route{route}
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	c := w.s.Fleet.Card(w.s.Work.Card("s1-1").F("work"))
	require.NotNil(t, c)
	require.Equal(t, route.Name, c.F(FieldRoute), "the take is dealt on the route")
	w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(Finish(w.s, FinishReq{
		Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID), Failed: true,
		Report: "no result: the child ended without a result", Usage: usage,
	}))
	v := CardCostOf(w.s.Work.Card("s1-1"))
	var got Consumer
	for _, con := range v.Consumers {
		if con.End == "no result" {
			got = con
		}
	}
	require.Equal(t, "no result", got.End, "the ended take is on the producer")
	return got.Usage, StreamTierCosts(w.s)["s1"]
}

func TestARunWithNoResultIsPricedOrCarriesItsReason(t *testing.T) {
	t.Parallel()

	t.Run("a usage line prices the run", func(t *testing.T) {
		t.Parallel()
		u, tc := noResultCost(t, pricedRoute, "input=1000000")
		assert.Equal(t, "pro-a", u.Route)
		assert.Equal(t, "1", u.Predicted)
		assert.False(t, u.Estimated)
		assert.Empty(t, u.Unpriced)
		assert.Equal(t, cardcost.CostPredicted, u.Present())
		assert.Equal(t, "$1.00", tc.TotalCost)
		assert.Equal(t, "$1.00", tc.WorkCost)
		assert.Equal(t, 0, tc.UnpricedRuns)
	})

	t.Run("prompt bytes are estimated and counted", func(t *testing.T) {
		t.Parallel()
		u, tc := noResultCost(t, pricedRoute, "prompt_bytes=4000000")
		assert.Equal(t, "pro-a", u.Route)
		assert.Equal(t, int64(4000000), u.PromptBytes)
		assert.False(t, u.Tokens.Reported(), "the estimate is not written as harness tokens")
		assert.True(t, u.Estimated)
		assert.Equal(t, "1", u.Predicted)
		assert.Empty(t, u.Unpriced)
		assert.Equal(t, "$1.00", tc.TotalCost)
		assert.Equal(t, 0, tc.UnpricedRuns)
	})

	t.Run("a usage line wins over prompt bytes", func(t *testing.T) {
		t.Parallel()
		u, tc := noResultCost(t, pricedRoute, "input=1000000 prompt_bytes=8")
		assert.Equal(t, int64(1000000), u.Tokens.Input)
		assert.False(t, u.Estimated)
		assert.Equal(t, "1", u.Predicted)
		assert.Empty(t, u.Unpriced)
		assert.Equal(t, 0, tc.UnpricedRuns)
	})

	t.Run("a generation quote prices the run", func(t *testing.T) {
		t.Parallel()
		u, tc := noResultCost(t, pricedRoute, "generation_id=gen-1 generation_input=2000000 generation_usd=0.5")
		assert.Equal(t, int64(2000000), u.Tokens.Input)
		assert.Equal(t, "0.5", u.Actual)
		assert.Equal(t, cardcost.ActualByGeneration, u.ActualBy)
		assert.Equal(t, "gen-1", u.GenerationID)
		assert.Empty(t, u.GenerationUSD)
		assert.False(t, u.Estimated)
		assert.Empty(t, u.Unpriced)
		assert.Equal(t, cardcost.CostBoth, u.Present())
		assert.Equal(t, "$0.50", tc.TotalCost)
		assert.Equal(t, 0, tc.UnpricedRuns)
	})

	t.Run("a route with no price table carries the reason", func(t *testing.T) {
		t.Parallel()
		bare := pricedRoute
		bare.Prices = cardcost.Prices{}
		u, tc := noResultCost(t, bare, "prompt_bytes=4000000")
		assert.Equal(t, cardcost.WhyNoSheet, u.Unpriced)
		assert.False(t, u.Estimated)
		assert.Empty(t, u.Predicted)
		assert.Empty(t, tc.TotalCost)
		assert.Empty(t, tc.WorkCost)
		assert.Equal(t, 1, tc.UnpricedRuns)
	})

	t.Run("no token and no prompt stays no-tokens", func(t *testing.T) {
		t.Parallel()
		u, tc := noResultCost(t, pricedRoute, "usage_source=none")
		assert.Equal(t, cardcost.WhyNoTokens, u.Unpriced)
		assert.False(t, u.Estimated)
		assert.Equal(t, cardcost.CostNone, u.Present())
		assert.Equal(t, 1, tc.UnpricedRuns)
		assert.Empty(t, tc.TotalCost)
	})
}
