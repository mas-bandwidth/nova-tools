package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOpenRouterListIsPerMillionExactly(t *testing.T) {
	t.Parallel()

	list, err := ParseOpenRouterList([]byte(`{"data":[
	 {"id":"a/one","pricing":{"prompt":"0.0000003","completion":"0.0000012","input_cache_read":"0.000000006","input_cache_write":"0.00000375","request":"0.005"}},
	 {"id":"a/free","pricing":{"prompt":"0","completion":"0","request":"0"}},
	 {"id":"openrouter/auto","pricing":{"prompt":"-1","completion":"-1"}},
	 {"id":"a/unpriced","pricing":{"image":"0.001"}}
	]}`))
	require.NoError(t, err)
	assert.Equal(t, ListPrice{Input: "0.3", CacheRead: "0.006", CacheWrite: "3.75", Output: "1.2", Request: "0.005"}, list["a/one"])
	assert.Equal(t, ListPrice{Input: "0", Output: "0"}, list["a/free"], "a free model is priced 0 and no request fee")
	assert.NotContains(t, list, "openrouter/auto", "a router's -1 is no price")
	assert.NotContains(t, list, "a/unpriced", "a model with no token price is left out")

	for _, body := range []string{`{"data":[]}`, `not json`, `{"data":[{"id":"x","pricing":{"prompt":"-1"}}]}`} {
		_, err := ParseOpenRouterList([]byte(body))
		assert.Error(t, err, body)
	}
}

func TestPriceJumpAndStaleAreExact(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		have, list  string
		jump, stale bool
	}{
		{"0.03", "0.3", true, true}, // the 2026-10-05 flash row: a tenth of the list
		{"0.5", "1.2", true, true},
		{"0.6", "1.2", false, true}, // exactly half is not past 2x
		{"2.4", "1.2", false, true}, // exactly twice is not past 2x
		{"2.41", "1.2", true, true},
		{"1.32", "1.2", false, false}, // exactly 10 percent is current
		{"1.3201", "1.2", false, true},
		{"1.08", "1.2", false, false},
		{"1.0799", "1.2", false, true},
		{"0", "0.3", true, true},
		{"", "0.3", false, true}, // a first price is never a jump, and a row with none is stale
		{"", "", false, false},
	} {
		assert.Equal(t, tc.jump, PriceJump(tc.have, tc.list), "jump %s -> %s", tc.have, tc.list)
		assert.Equal(t, tc.stale, PriceStale(tc.have, tc.list), "stale %s vs %s", tc.have, tc.list)
	}
}

func TestPlanPriceRefreshMatchesAModelWithoutItsVendorOnlyWhenOneAnswers(t *testing.T) {
	t.Parallel()

	list := map[string]ListPrice{"a/m1": {Input: "1", Output: "2"}, "a/m2": {Input: "1", Output: "2"}, "b/m2": {Input: "3", Output: "4"}}
	rows := []Row{
		{Name: "r1", Fields: map[string]string{"provider": "opencode", "model": "m1", "enabled": "true"}},
		{Name: "r2", Fields: map[string]string{"provider": "opencode", "model": "m2", "enabled": "true"}},
		{Name: "r3", Fields: map[string]string{"provider": "openrouter", "model": "b/m2", "enabled": "true"}},
	}
	plan := PlanPriceRefresh(rows, list, "", OpenRouterModelsURL, "2026-10-06")
	require.Len(t, plan, 3)
	assert.Equal(t, "a/m1", plan[0].ListID)
	assert.Equal(t, ProviderOpenRouter, plan[0].Assumed)
	assert.True(t, plan[1].Missing, "m2 is two models on the list: neither is guessed")
	assert.Equal(t, map[string]string{"price_input": "3", "price_output": "4", "price_as_of": "2026-10-06", "price_source": OpenRouterModelsURL}, plan[2].Changes)

	only := PlanPriceRefresh(rows, list, ProviderOpenRouter, OpenRouterModelsURL, "2026-10-06")
	require.Len(t, only, 1)
	assert.Equal(t, "r3", only[0].Route)
}
