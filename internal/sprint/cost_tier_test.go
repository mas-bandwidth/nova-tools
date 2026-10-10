package sprint

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// A run with no recorded tier is counted under its route's tier: the route row's, else the
// route name's prefix, else the card attempt's; "no tier" only when none of these names
// one; and the stream's tiers sum to its total, the records past the list's bound included
// (the owner, 2026-10-05: "The totals are inaccurate anyway").
func TestCostByTierTakesTheRouteTierWhenTheRunRecordsNone(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Routes = []Route{{Name: "or-big", Tier: "heavy", Provider: "openrouter", Model: "m"}}
	w.s.Work.SetRows([]string{"s1"})
	at := stamp(w.s.Now.Add(-time.Hour))
	pr := &Card{ID: "s1-1", Row: "s1", Col: Working, Fields: map[string]string{FieldTier: "flash"}}
	book(pr,
		Consumer{Kind: "work", Card: "s1-1", Key: "a", Tier: "frontier", Route: "or-big", End: "ok", At: at, Usage: cardcost.ParseUsage("input=1 actual_usd=1 actual_by=harness")},
		Consumer{Kind: "work", Card: "s1-1", Key: "b", Route: "or-big", End: "no result", At: at, Usage: cardcost.ParseUsage("input=1 actual_usd=2 actual_by=harness")},
		Consumer{Kind: "read", Card: "s1-1.r1", Key: "c", Route: "pro-x", End: "ok", At: at, Usage: cardcost.ParseUsage("input=1 predicted_usd=4")},
		Consumer{Kind: "work", Card: "s1-1", Key: "d", End: "failed", At: at, Usage: cardcost.ParseUsage("input=1 actual_usd=8 actual_by=harness")},
	)
	w.s.Work.Put(pr)
	bare := &Card{ID: "s1-2", Row: "s1", Col: Working, Fields: map[string]string{}}
	book(bare, Consumer{Kind: "work", Card: "s1-2", Key: "e", Route: "odd", End: "ok", At: at, Usage: cardcost.ParseUsage("input=1 actual_usd=16 actual_by=harness")})
	w.s.Work.Put(bare)

	tc := StreamTierCosts(w.s)["s1"]
	assert.Equal(t, map[string]string{
		"frontier": "$1.00",  // the tier it recorded
		"heavy":    "$2.00",  // the route row's
		"pro":      "$4.00",  // the route name's prefix
		"flash":    "$8.00",  // no route: the card attempt's
		"no tier":  "$16.00", // none of them
	}, tc.CostByTier)
	assert.Equal(t, "$31.00", tc.TotalCost)

	// past the list's bound: the records the total holds and the list does not take the card's tier
	many := &Card{ID: "s1-3", Row: "s1", Col: Working, Fields: map[string]string{FieldTier: "pro"}}
	for i := range MaxCostRecords + 3 {
		book(many, Consumer{Kind: "work", Card: "s1-3", Key: "k" + itoa(i), End: "ok", At: at, Tier: "flash", Usage: cardcost.ParseUsage("input=1 actual_usd=1 actual_by=harness")})
	}
	w.s.Work.Put(many)
	tc = StreamTierCosts(w.s)["s1"]
	sum := new(big.Rat)
	for _, v := range tc.CostByTier {
		r, ok := new(big.Rat).SetString(v[1:])
		require.True(t, ok, v)
		sum.Add(sum, r)
	}
	total, ok := new(big.Rat).SetString(tc.TotalCost[1:])
	require.True(t, ok)
	assert.Equal(t, total.FloatString(2), sum.FloatString(2), "every dollar of the total is in one tier: %v", tc.CostByTier)
	assert.Equal(t, "$7.00", tc.CostByTier["pro"], "the read on pro-x and the three records past the bound, on the card's tier")
}

// Displayed tier amounts allocate the stream's rounded-up total once, so rounding
// fractional cents never creates dollars the stream did not spend.
func TestCostByTierAllocatesFractionalCentsWithoutChangingTheTotal(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		amounts map[string]string
		want    map[string]string
		total   string
	}{
		{"two equal fractions", map[string]string{"flash": "0.001", "pro": "0.001"}, map[string]string{"flash": "$0.01", "pro": "$0.00"}, "$0.01"},
		{"larger fraction receives the cent", map[string]string{"flash": "0.001", "pro": "0.009"}, map[string]string{"flash": "$0.00", "pro": "$0.01"}, "$0.01"},
		{"whole cents stay with their tier", map[string]string{"flash": "1.001", "pro": "2.009"}, map[string]string{"flash": "$1.00", "pro": "$2.01"}, "$3.01"},
		{"more than one residual cent", map[string]string{"flash": "0.009", "heavy": "0.008", "pro": "0.007"}, map[string]string{"flash": "$0.01", "heavy": "$0.01", "pro": "$0.01"}, "$0.03"},
		{"exact cents", map[string]string{"flash": "0.01", "pro": "0.02"}, map[string]string{"flash": "$0.01", "pro": "$0.02"}, "$0.03"},
		{"zero spend", map[string]string{"flash": "0", "pro": "0"}, map[string]string{"flash": "$0.00", "pro": "$0.00"}, "$0.00"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			w.s.Work.SetRows([]string{"s1"})
			pr := &Card{ID: "s1-1", Row: "s1", Col: Working, Fields: map[string]string{}}
			for tier, amount := range tt.amounts {
				book(pr, Consumer{Kind: "work", Card: pr.ID, Key: tier, Tier: tier, End: "ok", At: stamp(w.s.Now), Usage: cardcost.ParseUsage("input=1 actual_usd=" + amount + " actual_by=harness")})
			}
			w.s.Work.Put(pr)
			tc := StreamTierCosts(w.s)["s1"]
			assert.Equal(t, tt.want, tc.CostByTier)
			assert.Equal(t, tt.total, tc.TotalCost)
			sum := new(big.Rat)
			for _, amount := range tc.CostByTier {
				usd, ok := new(big.Rat).SetString(amount[1:])
				require.True(t, ok)
				sum.Add(sum, usd)
			}
			assert.Equal(t, tc.TotalCost, "$"+sum.FloatString(2), "displayed tiers must sum to the displayed stream total")
		})
	}
}
