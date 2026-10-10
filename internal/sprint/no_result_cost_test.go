package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// TestARunWithNoResultIsPricedOrCarriesItsReason is the pure terminal-finish cost
// contract on the in-memory twin (docs/SPEC-SPRINT.md, "Every run's cost, whatever
// its end"): a run that ended without a result is still billed by the provider, so
// its record is priced from the usage that came, else from the provider's
// per-request quote it carried, else from the launch prompt's own bytes; when no
// price can be named the record carries the reason and the stream counts it. No
// path invents a zero-dollar charge.
func TestARunWithNoResultIsPricedOrCarriesItsReason(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, usage, charged, why string
		sheet                     bool
		estimated                 bool
	}{
		{"reported usage", "input=500000 output=50000", "1", "", true, false},
		{"provider quote", "generation_id=gen-one generation_input=500000 generation_output=50000 generation_usd=0.75", "0.75", "", true, false},
		{"prompt estimate", "prompt_bytes=400", "0.0001", "", true, true},
		{"no price table", "prompt_bytes=400", "", cardcost.WhyNoSheet, false, false},
		{"actual without tokens", "actual_usd=0.5 actual_by=harness", "0.5", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := setup(t, 1)
			route := pricedRoute
			if !tc.sheet {
				route.Prices = cardcost.Prices{}
			}
			w.s.Routes = []Route{route}
			w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
			c := w.s.Fleet.Card(w.s.Work.Card("s1-1").F("work"))
			w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
			w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID), Failed: true, Report: "no result: provider ended", Usage: tc.usage}))
			view := CardCostOf(w.s.Work.Card("s1-1"))
			require.Len(t, view.Consumers, 1)
			u := view.Consumers[0].Usage
			assert.Equal(t, tc.charged, view.Total.Charged)
			assert.Equal(t, tc.why, u.Unpriced)
			assert.Equal(t, tc.estimated, strings.Contains(u.String(), "estimated=yes"))
			stream := StreamTierCosts(w.s)["s1"]
			if tc.charged == "" {
				assert.Equal(t, 1, stream.UnpricedRuns)
			} else {
				assert.Zero(t, stream.UnpricedRuns)
				assert.Equal(t, MoneyText(tc.charged), stream.TotalCost)
			}
		})
	}
}
