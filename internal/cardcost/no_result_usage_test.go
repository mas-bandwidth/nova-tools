package cardcost

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

// Provider amounts remain exact and native token subsets are not charged twice.
func TestNoResultGenerationUsageIsExactAndRoundTrips(t *testing.T) {
	t.Parallel()
	g, err := ParseOpenRouterGeneration([]byte(`{"data":{"id":"gen-one","model":"vendor/m","native_tokens_prompt":100,"native_tokens_cached":20,"native_tokens_completion":40,"native_tokens_reasoning":10,"total_cost":0.000000123456789}}`))
	require.NoError(t, err)
	assert.Equal(t, int64(80), g.PromptTokens)
	assert.Equal(t, int64(20), g.CacheReadTokens)
	assert.Equal(t, int64(30), g.OutputTokens)
	assert.Equal(t, int64(10), g.ReasoningTokens)
	assert.Equal(t, "0.000000123456789", g.CostUSD)
	u := NoUsage().ApplyGeneration(g)
	assert.True(t, u.HasActual())
	assert.Equal(t, u, ParseUsage(u.String()))
	for _, body := range []string{`{"data":{}}`, `{"data":{"total_cost":-1}}`, `{"data":{"native_tokens_prompt":3,"native_tokens_cached":4}}`, `{"data":{"total_cost":1}} {}`, `{"data":{"total_cost":1e1000000000}}`} {
		_, err := ParseOpenRouterGeneration([]byte(body))
		assert.Error(t, err, body)
	}
}

func TestNoResultPromptEstimateKeepsReportedTokensUnknown(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		bytes    int64
		prices   Prices
		usd, why string
	}{
		{"rounded bytes", 5, Prices{Input: "4"}, "0.000008", ""},
		{"zero input price", 4, Prices{Input: "0"}, "0", ""},
		{"no sheet", 4, Prices{}, "", WhyNoSheet},
		{"no input price", 4, Prices{Output: "1"}, "", WhyNoPrice + "input"},
		{"bad price", 4, Prices{Input: "-1"}, "", WhyBadPrice + "input"},
		{"long and gateway", 44, Prices{Input: "1", LongContext: 10, InputLong: "2", GatewayPercent: "10"}, "0.0000242", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u := NoUsage()
			u.PromptBytes = tc.bytes
			u = u.EstimatedFrom("route-a", tc.prices)
			assert.Equal(t, tc.usd, u.Predicted)
			assert.Equal(t, tc.why, u.Unpriced)
			assert.Equal(t, tc.usd != "", u.Estimated)
			assert.Equal(t, None(), u.Tokens)
			assert.Equal(t, u, ParseUsage(u.String()))
		})
	}
}
