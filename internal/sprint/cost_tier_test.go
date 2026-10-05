package sprint

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// The per-tier split of a stream's spend (cost_view.go, CostByTier; the owner, 2026-10-05:
// "The totals are inaccurate anyway") accounts for every dollar: a run that recorded no tier
// takes its route's (the route row's, else the tier its name begins with), else the card's;
// "untiered" only when none of these exists; the records past a card's list bound take the
// card's tier; and the tiers sum to the stream's total to the cent.
func TestCostByTierTakesTheRouteTierWhenTheRunRecordsNone(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Routes = []Route{{Name: "or-a", Tier: "pro", Provider: "openrouter", Model: "m"}}
	w.s.Work.SetRows([]string{"s1"})
	at := stamp(w.s.Now)
	run := func(key, route, tier, usd string) Consumer {
		return Consumer{Kind: "work", Card: "s1", Key: key, Route: route, Tier: tier, End: "failed", At: at, Usage: cardcost.ParseUsage("input=1 actual_usd=" + usd + " actual_by=harness")}
	}
	// a card pinned to nothing and dealt nothing: its runs' tiers come from their routes
	plain := &Card{ID: "s1-1", Row: "s1", Col: Working, Fields: map[string]string{}}
	book(plain,
		run("a", "or-a", "", "1.001"),       // the route row's tier: pro
		run("b", "heavy-x", "", "2.002"),    // no row: the name's prefix, heavy
		run("c", "frontier-y", "", "0.333"), // frontier
		run("d", "flash-z", "", "0.333"),    // flash
		run("e", "or-a", "frontier", "0.5"), // a recorded tier stands
		run("f", "", "", "0.25"),            // nothing at all: untiered
		run("g", "mystery", "", "0.75"),     // a route of no row and no prefix: untiered
		run("h", "", "", "0.0001"),          // a hundredth of a cent, untiered
	)
	w.s.Work.Put(plain)
	// a card dealt on pro: its runs with no tier and no known route ran on pro, and so did
	// the record past its list's bound, which is in its total and in no list
	dealt := &Card{ID: "s1-2", Row: "s1", Col: Working, Fields: map[string]string{FieldTierNow: "pro"}}
	book(dealt, run("i", "mystery", "", "3"), run("j", "", "", "1.111"), run("cut", "", "", "4"))
	delete(dealt.Fields, FieldCostRecord+"cut")
	dealt.Fields[FieldCostCut] = "1"
	w.s.Work.Put(dealt)

	got := StreamTierCosts(w.s)["s1"]
	assert.Equal(t, map[string]string{
		"pro":      "$9.11", // 1.001 + 3 + 1.111 + 4 (the cut record) = 9.112
		"heavy":    "$2.00", // 2.002
		"frontier": "$0.84", // 0.833, and one of the two cents the rounding leaves
		"flash":    "$0.34", // 0.333, and the other (the largest remainders, ties by name)
		"untiered": "$1.00", // 0.25 + 0.75 + 0.0001
	}, got.CostByTier, "13.2801 is 1329 cents: each tier rounded down, the two cents left to the largest remainders")
	require.NotEmpty(t, got.TotalCost)
	sum := new(big.Rat)
	for _, v := range got.CostByTier {
		r, ok := new(big.Rat).SetString(v[1:])
		require.True(t, ok, v)
		sum.Add(sum, r)
	}
	assert.Equal(t, got.TotalCost, cardcost.Cents(sum), "the tiers sum to the stream's total, to the cent")
	assert.Equal(t, "$13.29", got.TotalCost) // 13.2801 rounded up
}
