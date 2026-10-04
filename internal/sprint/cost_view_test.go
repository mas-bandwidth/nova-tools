package sprint

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// costRules is a route table of three routes: two tiers' routes and one model two tiers run.
func costRules() TierRules {
	return RulesOf([]Route{
		{Name: "grok47-openrouter", Tier: "pro", Provider: "openrouter", Model: "x-ai/grok-4.7"},
		{Name: "flash-q", Tier: "flash", Provider: "opencode", Model: "qwen3.8-flash"},
		{Name: "pro-both", Tier: "pro", Provider: "opencode", Model: "both-1"},
		{Name: "flash-both", Tier: "flash", Provider: "opencode", Model: "both-1"},
	})
}

// A cost record's tier (RecordTier, cost_view.go) is always one of the four: its own;
// else its route's in the route table; else its route name's first word when that is a
// tier; else its model's in the route table; else its model family's; else its card's.
func TestRecordTierIsAlwaysATier(t *testing.T) {
	t.Parallel()
	r := costRules()
	pr := &Card{ID: "s1-1", Fields: map[string]string{"brief": "RESULT: s1-1 tier: pro", FieldTierNow: "flash"}}
	tierOf := func(con Consumer) []any {
		tier, byCard := RecordTier(con, pr, r)
		return []any{tier, byCard}
	}
	assert.Equal(t, []any{"flash", false}, tierOf(Consumer{Tier: "flash", Route: "pro-grok47-opencode"}), "its own tier first")
	assert.Equal(t, []any{"pro", false}, tierOf(Consumer{Tier: "untiered", Route: "grok47-openrouter"}), "a word that is no tier is not its own")
	assert.Equal(t, []any{"pro", false}, tierOf(Consumer{Route: "grok47-openrouter"}), "the route table's tier")
	assert.Equal(t, []any{"heavy", false}, tierOf(Consumer{Route: "heavy-claude-studio"}), "the route name's first word")
	assert.Equal(t, []any{"flash", false}, tierOf(Consumer{Route: "gone-route", Model: "opencode/qwen3.8-flash"}), "the model's route's tier")
	assert.Equal(t, []any{"heavy", false}, tierOf(Consumer{Model: "anthropic/claude-opus-5.5"}), "the model's family")
	assert.Equal(t, []any{"frontier", false}, tierOf(Consumer{Model: "anthropic/claude-fable-5"}), "frontier before heavy")
	assert.Equal(t, []any{"flash", true}, tierOf(Consumer{Model: "opencode/both-1"}), "a model two tiers run names neither: the card's")
	assert.Equal(t, []any{"flash", true}, tierOf(Consumer{}), "nothing named: the tier the card is on")
	assert.Equal(t, "pro", CardTier(&Card{Fields: map[string]string{"brief": "RESULT: x tier: pro"}}), "no tier_now: the brief's")
}

// SplitCents is the tiers in dollars and cents adding up to their sum rounded up to the
// cent: thirds of 99.9 cents are 33, 33 and 34 cents, never three times 34.
func TestSplitCentsAddsUpToTheRoundedSum(t *testing.T) {
	t.Parallel()
	third := big.NewRat(333, 1000)
	got := SplitCents(map[string]*big.Rat{"flash": third, "pro": new(big.Rat).Set(third), "heavy": new(big.Rat).Set(third)})
	assert.Equal(t, map[string]string{"flash": "$0.34", "pro": "$0.33", "heavy": "$0.33"}, got)
	assert.Equal(t, map[string]string{"pro": "$1.24"}, SplitCents(map[string]*big.Rat{"pro": big.NewRat(12345, 10000)}), "one tier: MoneyText's rounding up")
	assert.Empty(t, SplitCents(map[string]*big.Rat{"pro": new(big.Rat)}), "no cent, no key")
}

// The owner, 2026-10-04 4:20 PM: a stream's tiers must add up to its cost. A stream's
// cost_by_tier (StreamTierCosts) splits its landed cost: a record with its own tier, one
// with a route only, one with a model only, a read, a record with nothing named, and the
// records past the list's bound (in the card's cost, not in its list) each land in one
// tier, and the tiers' cents add up to the cost cell. A card not landed is in no column.
// No key is anything but a tier.
func TestStreamTierCostsAddUpToTheCost(t *testing.T) {
	t.Parallel()
	rec := func(kind, card, route, model, tier, usd string) string {
		return Consumer{Kind: kind, Card: card, Attempt: 1, Who: "m1", Route: route, Model: model, Tier: tier, End: "ok", At: "2026-10-04T15:00:00Z",
			Usage: cardcost.Usage{Actual: usd, ActualBy: cardcost.ActualByHarness}}.line()
	}
	w := NewTable(Work)
	w.SetRows([]string{"s1"})
	w.Put(&Card{ID: "s1-1", Row: "s1", Col: Landed, Fields: map[string]string{
		"brief":                   "RESULT: s1-1 tier: pro",
		FieldTierNow:              "pro",
		FieldCost:                 "10.0105", // 7.0035 listed, 3.007 past the list's bound
		FieldCostCut:              "3",
		FieldCostRecord + "tier":  rec("work", "s1-1.w1", "flash-gone", "", "flash", "1.001"),
		FieldCostRecord + "route": rec("work", "s1-1.w2", "grok47-openrouter", "", "", "2.0005"),
		FieldCostRecord + "model": rec("work", "s1-1.w3", "", "anthropic/claude-sonnet-5.5", "", "3.001"),
		FieldCostRecord + "read":  rec("read", "s1-1.r1", "flash-q", "opencode/qwen3.8-flash", "", "0.001"),
		FieldCostRecord + "none":  rec("work", "s1-1.w4", "", "", "", "1"),
	}})
	w.Put(&Card{ID: "s1-2", Row: "s1", Col: Landed, Fields: map[string]string{
		"brief":                  "RESULT: s1-2 tier: flash",
		FieldCost:                "0.3333",
		FieldCostRecord + "read": rec("read", "s1-2.r1", "", "opencode/qwen3.8-flash", "", "0.3333"),
	}})
	w.Put(&Card{ID: "s1-3", Row: "s1", Col: Working, Fields: map[string]string{
		"brief":               "RESULT: s1-3 tier: pro",
		FieldCostRecord + "a": rec("work", "s1-3.w1", "grok47-openrouter", "", "", "50"),
	}})
	got := StreamTierCosts(&Snapshot{Work: w}, costRules())["s1"]
	assert.Equal(t, map[string]string{"flash": "$1.34", "pro": "$6.01", "heavy": "$3.00"}, got.CostByTier,
		"flash: its own 1.001, the read 0.001, the flash card's read 0.3333; pro: the route's 2.0005, nothing named 1, past the list 3.007; heavy: Sonnet 3.001")
	cost, ok := cardcost.Sum("10.0105", "0.3333")
	require.True(t, ok)
	sum := new(big.Rat)
	for tier, v := range got.CostByTier {
		assert.True(t, IsTier(tier), "only tiers are keys: %q", tier)
		r, ok := new(big.Rat).SetString(v[1:])
		require.True(t, ok)
		sum.Add(sum, r)
	}
	assert.Equal(t, MoneyText(cost), cardcost.Cents(sum), "sum(cost_by_tier) == cost")
	assert.NotContains(t, got.CostByTier, "untiered")
	assert.Equal(t, map[string]int{"pro": 2, "flash": 1}, got.Tiers)
}
