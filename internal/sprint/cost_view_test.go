package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// A cost record's tier (RouteTierOf, cost_view.go): its own when it carries one, else its
// route's in the route table, else the route name's first word when that is a tier. A
// record none of them names has no tier: ok is false, never a made-up tier.
func TestRouteTierOfFallsBackToTheRoute(t *testing.T) {
	t.Parallel()
	routes := map[string]string{"grok47-openrouter": "pro"}
	tierOf := func(con Consumer, routes map[string]string) []any {
		tier, ok := RouteTierOf(con, routes)
		return []any{tier, ok}
	}
	assert.Equal(t, []any{"flash", true}, tierOf(Consumer{Tier: "flash", Route: "pro-grok47-opencode"}, routes), "its own tier first")
	assert.Equal(t, []any{"pro", true}, tierOf(Consumer{Route: "grok47-openrouter"}, routes), "the route table's tier")
	assert.Equal(t, []any{"pro", true}, tierOf(Consumer{Route: "pro-grok47-opencode"}, routes), "the route name's first word")
	assert.Equal(t, []any{"heavy", true}, tierOf(Consumer{Route: "heavy-claude-studio"}, nil))
	assert.Equal(t, []any{"", false}, tierOf(Consumer{Route: "deepseek-opencode"}, nil), "a first word that is no tier")
	assert.Equal(t, []any{"", false}, tierOf(Consumer{}, routes), "no route")
}

// A stream's spend by tier (StreamTierCosts) has only tiers for keys: a record whose tier
// cannot be found is left out of cost_by_tier and counted, with its route, in the
// diagnostic NoTierCost and NoTierRoutes.
func TestStreamTierCostsHasNoBucketForARecordWithoutATier(t *testing.T) {
	t.Parallel()
	rec := func(card, route, tier, usd string) string {
		return Consumer{Kind: "work", Card: card, Attempt: 1, Who: "m1", Route: route, Tier: tier, End: "ok", At: "2026-10-04T15:00:00Z",
			Usage: cardcost.Usage{Actual: usd, ActualBy: cardcost.ActualByHarness}}.line()
	}
	w := NewTable(Work)
	w.SetRows([]string{"s1"})
	w.Put(&Card{ID: "s1-1", Row: "s1", Col: Ready, Fields: map[string]string{
		"brief":                  "RESULT: s1-1 tier: pro",
		FieldCostRecord + "a":    rec("s1-1.w1", "flash-a", "flash", "1"),
		FieldCostRecord + "b":    rec("s1-1.w2", "pro-b", "", "2"),
		FieldCostRecord + "c":    rec("s1-1.r1", "grok47-openrouter", "", "4"),
		FieldCostRecord + "d":    rec("s1-1.r2", "mystery-route", "", "0.25"),
		FieldCostRecord + "none": rec("s1-1.r3", "", "", "0.5"),
	}})
	got := StreamTierCosts(&Snapshot{Work: w}, map[string]string{"grok47-openrouter": "pro"})["s1"]
	assert.Equal(t, map[string]string{"flash": "$1.00", "pro": "$6.00"}, got.CostByTier, "only tiers: the route table's and the route name's")
	assert.Equal(t, "$0.75", got.NoTierCost, "the records no route names a tier for")
	assert.Equal(t, []string{"-", "mystery-route"}, got.NoTierRoutes)
	assert.Equal(t, map[string]int{"pro": 1}, got.Tiers)
}
