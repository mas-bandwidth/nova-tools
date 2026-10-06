package config

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPerMillionExactArithmetic(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   any
		want string
	}{
		{"0.00000030", "0.3"},
		{"0.00000120", "1.2"},
		{"0.000000006", "0.006"},
		{"0", "0"},
		{0.0000003, "0.3"},
		{nil, ""},
		{"", ""},
	}
	for _, tc := range cases {
		got, err := PerMillion(tc.in)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got, "PerMillion(%v)", tc.in)
	}
}

func TestParseOpenRouterPrices(t *testing.T) {
	t.Parallel()

	jsonStr := `{
		"data": [
			{
				"id": "deepseek/deepseek-v4.1-flash",
				"pricing": {
					"prompt": "0.00000030",
					"completion": "0.00000120",
					"input_cache_read": "0.000000006"
				}
			},
			{
				"id": "x-ai/grok-4",
				"pricing": {
					"prompt": "0.00000300",
					"completion": "0.00001500"
				}
			}
		]
	}`

	catalog, err := ParseOpenRouterPrices(strings.NewReader(jsonStr))
	require.NoError(t, err)
	require.Len(t, catalog, 2)

	ds := catalog["deepseek/deepseek-v4.1-flash"]
	assert.Equal(t, "0.3", ds.Input)
	assert.Equal(t, "1.2", ds.Output)
	assert.Equal(t, "0.006", ds.CacheRead)
	assert.Equal(t, OpenRouterModelsURL, ds.Source)

	grok := catalog["x-ai/grok-4"]
	assert.Equal(t, "3", grok.Input)
	assert.Equal(t, "15", grok.Output)
	assert.Equal(t, "", grok.CacheRead)
}

func TestMatchPrices(t *testing.T) {
	t.Parallel()

	catalog := map[string]RoutePrices{
		"deepseek/deepseek-v4.1-flash": {Input: "0.3", Output: "1.2", CacheRead: "0.006"},
		"x-ai/grok-4":                  {Input: "3", Output: "15"},
	}

	// Exact match
	p, ok := MatchPrices(catalog, "openrouter", "deepseek/deepseek-v4.1-flash")
	require.True(t, ok)
	assert.Equal(t, "0.3", p.Input)

	// Opencode without organization prefix
	p, ok = MatchPrices(catalog, "opencode", "deepseek-v4.1-flash")
	require.True(t, ok)
	assert.Equal(t, "0.3", p.Input)

	// Not found
	_, ok = MatchPrices(catalog, "openrouter", "nonexistent-model")
	assert.False(t, ok)
}

func TestCheckOver2x(t *testing.T) {
	t.Parallel()

	// Empty old price: not over 2x (initial set)
	_, _, _, over := CheckOver2x(map[string]string{}, RoutePrices{Input: "0.3", Output: "1.2"})
	assert.False(t, over)

	// Exactly 2x: not over 2x
	_, _, _, over = CheckOver2x(map[string]string{cardcost.FieldInput: "0.15"}, RoutePrices{Input: "0.3"})
	assert.False(t, over)

	// 2.01x: over 2x
	field, oldV, newV, over := CheckOver2x(map[string]string{cardcost.FieldInput: "0.14"}, RoutePrices{Input: "0.3"})
	assert.True(t, over)
	assert.Equal(t, cardcost.FieldInput, field)
	assert.Equal(t, "0.14", oldV)
	assert.Equal(t, "0.3", newV)

	// 6x: over 2x
	field, oldV, newV, over = CheckOver2x(map[string]string{cardcost.FieldInput: "0.05"}, RoutePrices{Input: "0.3"})
	assert.True(t, over)
	assert.Equal(t, cardcost.FieldInput, field)
	assert.Equal(t, "0.05", oldV)
	assert.Equal(t, "0.3", newV)

	// Price decrease: not over 2x
	_, _, _, over = CheckOver2x(map[string]string{cardcost.FieldInput: "0.50"}, RoutePrices{Input: "0.3"})
	assert.False(t, over)
}

func TestCheckPriceDiffersOver10Pct(t *testing.T) {
	t.Parallel()

	// Empty old price is stale
	assert.True(t, CheckPriceDiffersOver10Pct(map[string]string{}, RoutePrices{Input: "0.3"}))

	// Equal prices: not stale
	assert.False(t, CheckPriceDiffersOver10Pct(map[string]string{cardcost.FieldInput: "0.3", cardcost.FieldOutput: "1.2"}, RoutePrices{Input: "0.3", Output: "1.2"}))

	// 5% difference: not stale
	assert.False(t, CheckPriceDiffersOver10Pct(map[string]string{cardcost.FieldInput: "1.00"}, RoutePrices{Input: "1.05"}))

	// 10% exact difference: not stale (must be > 10%)
	assert.False(t, CheckPriceDiffersOver10Pct(map[string]string{cardcost.FieldInput: "1.00"}, RoutePrices{Input: "1.10"}))

	// 15% difference: stale
	assert.True(t, CheckPriceDiffersOver10Pct(map[string]string{cardcost.FieldInput: "1.00"}, RoutePrices{Input: "1.15"}))

	// 15% decrease: stale
	assert.True(t, CheckPriceDiffersOver10Pct(map[string]string{cardcost.FieldInput: "1.00"}, RoutePrices{Input: "0.85"}))
}
