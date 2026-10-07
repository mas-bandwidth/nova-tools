package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// cost reprice computes every priced record again from its tokens at its route's current
// prices (docs/SPEC-SPRINT.md, "What a card cost", the reprice): a dry run prints per route
// the old and new sums and writes nothing; the run rewrites each record's prediction and
// sheet and the card's total; the harness's own cost stays each record's charged figure,
// counted held; a second run at the same prices changes nothing.
func TestCostRepriceRewritesTheRecordsFromTheirTokens(t *testing.T) {
	t.Parallel()
	ta := costCard(t)
	rs := costRoutes()
	rs[0].Prices.Input, rs[0].Prices.CacheRead, rs[0].Prices.Output, rs[0].Prices.AsOf = "0.28", "0.056", "0.56", "2026-10-05"
	ta.m.SetRoutes(rs)
	before := ta.ok("card s1-1")

	dry := ta.dry("cost reprice --dry-run")
	assert.Contains(t, dry, "REPRICE route=flash-a price_as_of=2026-10-05 records=2 changed=2 old=$0.01 new=$0.01 old_usd=0.0014 new_usd=0.0014 old_predicted_usd=0.001204 new_predicted_usd=0.002408 held_by_actual=2\n")
	// the friend's read is priced by the route of the model she ran, pro-a, whose prices hold
	assert.Contains(t, dry, "REPRICE route=pro-a price_as_of=- records=1 changed=0 old=$0.01 new=$0.01 old_usd=0.0015 new_usd=0.0015 old_predicted_usd=0.0014 new_predicted_usd=0.0014 held_by_actual=1\n")
	assert.Contains(t, dry, "REPRICE LEFT no_tokens=0 subscription=0 no_route=0 cut=0\n")
	assert.Contains(t, dry, "COST REPRICE DRY-RUN routes=2 cards=1 streams=0: nothing was written\n")
	assert.Equal(t, before, ta.ok("card s1-1"), "a dry run writes nothing")

	out := ta.ok("cost reprice --route flash-a --since 2000-01-01T00:00:00Z")
	assert.Contains(t, out, "COST REPRICE OK routes=1 cards=1 streams=0\n")
	after := ta.ok("card s1-1")
	assert.Contains(t, after, "predicted_usd=0.003808 predicted_of=3/3 actual_usd=0.0029 actual_by=harness actual_of=3/3 charged_usd=0.0029")
	assert.Contains(t, after, "route=flash-a model=opencode/deepseek-v4-flash tier=flash end=failed input=1000 cache_read=2000 cache_write=- output=300 reasoning=200 requests=3 wait=2s run=10s predicted_usd=0.000672 actual_usd=0.0005")
	var v cardView
	ta.json("card s1-1", &v)
	require.Len(t, v.Cost.Consumers, 3)
	for _, c := range v.Cost.Consumers {
		if c.Kind == "work" {
			assert.Equal(t, "in:0.28,cr:0.056,out:0.56,ro:true,asof:2026-10-05", c.Usage.Prices, c.Key)
		}
	}

	var got struct {
		Routes []sprint.RepriceRoute `json:"routes"`
		Cards  int                   `json:"cards"`
		DryRun bool                  `json:"dry_run"`
	}
	ta.json("cost reprice", &got)
	require.Len(t, got.Routes, 2)
	for _, r := range got.Routes {
		assert.Equal(t, 0, r.Changed, "at the same prices nothing moves: %s", r.Route)
	}
	assert.Equal(t, 0, got.Cards)

	code, _, errOut := ta.do("cost reprice --since yesterday")
	assert.NotZero(t, code)
	assert.Contains(t, errOut, "RFC3339")
	code, _, errOut = ta.do("cost reprice --route nowhere")
	assert.NotZero(t, code)
	assert.Contains(t, errOut, "route nowhere is not in the store")
}
