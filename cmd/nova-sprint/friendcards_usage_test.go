package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// A friend's finish prices the usage she published (docs/SPEC-SPRINT.md, a friend's
// finish). RESULT.md's tokens: and cost: win over the report's Cost: headline; a
// finish with no token line stays unpriced and invents nothing.

const friendUsageModel = "opencode/deepseek-v4-flash"

func TestFriendFinishRefusesZeroTokensBeforeTheLedger(t *testing.T) {
	t.Parallel()
	report := "Verdict: LAND\nHead: abc\nCost: unpriced (zero tokens) tokens input=0 cache_read=0 cache_write=0 output=0 reasoning=0 model=opencode/flash harness=opencode\n"
	assert.Empty(t, friendFinishUsage(report))
	assert.Empty(t, friendFinishUsage("tokens: input=0 cache_read=0 cache_write=0 output=0 reasoning=0 model=opencode/flash harness=opencode\ncost: unpriced (zero tokens)\n"))
}

func TestFriendFinishRecordsZeroTokensAsUnpriced(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.m.SetRoutes(costRoutes())
	zeros := "input=0 cache_read=0 cache_write=0 output=0 reasoning=0 model=" + friendUsageModel + " harness=opencode"
	report := "Verdict: LAND\nHead: " + landHead + "\nCost: unpriced (zero tokens) tokens " + zeros + "\n"
	result := "tokens: " + zeros + "\ncost: unpriced (zero tokens)\n"
	friendLand(t, ta, root, report, result)
	got := friendConsumer(t, ta)
	assert.Equal(t, cardcost.WhyNoTokens, got.Unpriced)
	assert.Empty(t, got.Predicted)
	assert.Empty(t, got.Actual)
}

func friendLand(t *testing.T, ta *testApp, root, report, result string) {
	t.Helper()
	ta.ok("tick")
	ta.ok("friend sync --root " + root)
	outboxReport(t, root, "amy", "s1-1.w1", report)
	if result != "" {
		require.NoError(t, os.WriteFile(filepath.Join(root, "amy-working", "outbox", "s1-1.w1", "RESULT.md"), []byte(result), 0o644))
	}
	ta.ok("friend sync --root " + root)
	ta.ok("tick")
}

func friendConsumer(t *testing.T, ta *testApp) cardcost.Usage {
	t.Helper()
	var c cardView
	ta.json("card s1-1", &c)
	require.Len(t, c.Cost.Consumers, 1)
	return c.Cost.Consumers[0].Usage
}

func TestFriendFinishPricesResultTokensByTheRouteRow(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.m.SetRoutes(costRoutes())
	result := "tokens: input=1000 cache_read=0 cache_write=0 output=100 reasoning=0 model=" + friendUsageModel + " harness=opencode\n" +
		"cost: $0.25 (opencode: $0.02)\n"
	report := "Verdict: LAND\nHead: " + landHead + "\n\nPushed.\n" +
		"Cost: $9.99 (opencode: $1.00) tokens input=1 cache_read=0 cache_write=0 output=1 reasoning=0 model=opencode/deepseek-v4-pro harness=opencode price_route=pro-a\n"
	friendLand(t, ta, root, report, result)

	var c cardView
	ta.json("card s1-1", &c)
	require.Len(t, c.Cost.Consumers, 1)
	got := c.Cost.Consumers[0]
	assert.Equal(t, "flash-a", got.Route, "on_route is the route row whose provider/model matches")
	assert.Equal(t, friendUsageModel, got.Model)
	assert.Equal(t, int64(1000), got.Usage.Tokens.Input)
	assert.Equal(t, int64(0), got.Usage.Tokens.CacheRead)
	assert.Equal(t, int64(0), got.Usage.Tokens.CacheWrite)
	assert.Equal(t, int64(100), got.Usage.Tokens.Output)
	assert.Equal(t, "flash-a", got.Usage.Route)
	want := cardcost.Predict(got.Usage.Tokens, costRoutes()[0].Prices)
	assert.Equal(t, want.USD, got.Usage.Predicted)
	assert.Equal(t, "0.02", got.Usage.Actual, "the harness parenthetical, not the rounded headline")
	assert.Equal(t, cardcost.ActualByHarness, got.Usage.ActualBy)
	assert.Contains(t, ta.ok("card s1-1"), "route=flash-a")
	assert.Equal(t, int64(1100), whereFriends(ta)["amy"].Tokens)
}

func TestFriendFinishWithNoTokensStaysUnpriced(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.m.SetRoutes(costRoutes())
	friendLand(t, ta, root, "Verdict: LAND\nHead: "+landHead+"\n\nPushed.\n", "Verdict: LAND\n\nPushed, and no token line.\n")

	got := friendConsumer(t, ta)
	assert.Equal(t, cardcost.WhyNoTokens, got.Unpriced)
	assert.False(t, got.Tokens.Reported())
	assert.Equal(t, "", got.Predicted)
	assert.Equal(t, "", got.Actual)
	assert.Equal(t, int64(0), whereFriends(ta)["amy"].Tokens)
}

func TestFriendFinishPricesAReportCostHeadlineWhenResultHasNone(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.m.SetRoutes(costRoutes())
	report := "Verdict: LAND\nHead: " + landHead + "\n\nPushed.\n" +
		"Cost: $0.25 (opencode: $0.02) tokens input=1000 cache_read=0 cache_write=0 output=100 reasoning=0 model=" + friendUsageModel + " harness=opencode price_route=nope\n"
	friendLand(t, ta, root, report, "")

	var c cardView
	ta.json("card s1-1", &c)
	require.Len(t, c.Cost.Consumers, 1)
	got := c.Cost.Consumers[0]
	assert.Equal(t, "flash-a", got.Route, "on_route is the route row whose provider/model matches")
	assert.Equal(t, friendUsageModel, got.Model)
	assert.Equal(t, "flash-a", got.Usage.Route)
	assert.Equal(t, int64(1000), got.Usage.Tokens.Input)
	assert.Equal(t, int64(100), got.Usage.Tokens.Output)
	want := cardcost.Predict(got.Usage.Tokens, costRoutes()[0].Prices)
	assert.Equal(t, want.USD, got.Usage.Predicted)
	assert.Equal(t, "0.02", got.Usage.Actual)
}

func TestFriendFinishLeavesActualAbsentWhenHarnessCostIsUnreported(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, report, result string
	}{
		{
			name: "report headline",
			report: "Verdict: LAND\nHead: " + landHead + "\n\nPushed.\n" +
				"Cost: $9.99 (opencode: -) tokens input=1000 cache_read=0 cache_write=0 output=100 reasoning=0 model=" + friendUsageModel + " harness=opencode\n",
		},
		{
			name: "result cost line",
			report: "Verdict: LAND\nHead: " + landHead + "\n\nPushed.\n" +
				"Cost: $8.88 (opencode: $1.00) tokens input=1 cache_read=0 cache_write=0 output=1 reasoning=0 model=opencode/deepseek-v4-pro harness=opencode\n",
			result: "tokens: input=1000 cache_read=0 cache_write=0 output=100 reasoning=0 model=" + friendUsageModel + " harness=opencode\n" +
				"cost: $9.99 (opencode: -)\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ta, root := friendCardApp(t, "friend amy", "amy")
			ta.m.SetRoutes(costRoutes())
			friendLand(t, ta, root, tc.report, tc.result)

			var c cardView
			ta.json("card s1-1", &c)
			require.Len(t, c.Cost.Consumers, 1)
			got := c.Cost.Consumers[0]
			assert.Equal(t, "flash-a", got.Route, "on_route is the route row whose provider/model matches")
			assert.Equal(t, friendUsageModel, got.Model)
			assert.Equal(t, int64(1000), got.Usage.Tokens.Input)
			assert.Equal(t, int64(100), got.Usage.Tokens.Output)
			assert.Equal(t, "flash-a", got.Usage.Route)
			assert.Equal(t, "", got.Usage.Actual, "an unreported harness cost leaves actual absent")
			assert.Equal(t, "", got.Usage.ActualBy)
			assert.NotEqual(t, "9.99", got.Usage.Actual)
			want := cardcost.Predict(got.Usage.Tokens, costRoutes()[0].Prices)
			assert.Equal(t, want.USD, got.Usage.Predicted)
			assert.NotEmpty(t, got.Usage.Predicted)
		})
	}
}
