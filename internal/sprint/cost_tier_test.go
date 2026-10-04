package sprint

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// The friends row of tiers and of cost_by_tier carries token counts and no dollar
// field (docs/SPEC-SPRINT.md, the friends category). A dollar a friend reported is
// dropped, not shown and not moved onto a machine tier.

func TestFriendsCostIsTokenCountsOnly(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Work: NewTable(Work), Fleet: NewTable(Fleet), Routes: []Route{{Name: "flash-a", Tier: "flash"}}}
	s.Work.SetRows([]string{"s1"})
	pr := &Card{ID: "p1", Row: "s1", Col: Landed, Fields: map[string]string{"attempt": "1"}}
	book(pr,
		Consumer{Kind: "work", Card: "p1.w1", Who: FriendRow("amy"), Tier: "pro", End: "ok", At: "2026-10-04T12:00:01Z", Key: "p1.w1#g1",
			Usage: cardcost.ParseUsage("input=100 output=7 actual_usd=9.5 predicted_usd=8")},
		Consumer{Kind: "work", Card: "p1.w2", Who: FriendRow("bea"), End: "ok", At: "2026-10-04T12:00:02Z", Key: "p1.w2#g1",
			Route: "flash-a", Usage: cardcost.ParseUsage("input=5 cache_read=1")},
		Consumer{Kind: "work", Card: "p1.w3", Who: "m1", Tier: "flash", End: "ok", At: "2026-10-04T12:00:03Z", Key: "p1.w3#g1",
			Usage: cardcost.ParseUsage("input=3 cache_read=4 output=1 actual_usd=0.5")},
		Consumer{Kind: "read", Card: "p1.r1.reader-a", Who: "reader-a", Tier: "pro", End: "ok", At: "2026-10-04T12:00:04Z", Key: "p1.r1.reader-a#v",
			Usage: cardcost.ParseUsage("input=9 actual_usd=1.25")},
		Consumer{Kind: "work", Card: "p1.w4", Who: FriendRow("cy"), Tier: "flash", End: "ok", At: "2026-10-04T12:00:05Z", Key: "p1.w4#g1",
			Usage: cardcost.ParseUsage("actual_usd=4")},
	)
	s.Work.Put(pr)

	tiers, byTier := TierCosts(s)
	for _, rows := range [][]CostCategory{tiers, byTier} {
		friends, ok := category(rows, Friends)
		require.True(t, ok, "rows %v", names(rows))
		assert.Equal(t, int64(105), friends.Tokens.Input, "both friends' input, and not the machine's")
		assert.Equal(t, int64(7), friends.Tokens.Output)
		assert.Equal(t, int64(1), friends.Tokens.CacheRead)
		assert.Equal(t, int64(113), friends.Tokens.Total)
		assert.Empty(t, friends.USD)
		raw, err := json.Marshal(friends)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "usd")
		assert.NotContains(t, string(raw), "dollar")
		assert.NotContains(t, string(raw), "9.5")
		assert.NotContains(t, string(raw), "predicted")
		assert.NotContains(t, string(raw), "actual")
		assert.NotContains(t, string(raw), "charged")
		assert.Contains(t, string(raw), `"tier":"friends"`)
		assert.Contains(t, string(raw), `"input":105`)

		flash, ok := category(rows, "flash")
		require.True(t, ok)
		assert.Equal(t, "0.5", flash.USD, "the machine tier keeps its dollars")
		assert.Equal(t, int64(3), flash.Tokens.Input, "a friend's tokens are not on the tier")
		pro, ok := category(rows, "pro")
		require.True(t, ok)
		assert.Equal(t, "1.25", pro.USD, "a reader's dollars stay on the tier; the friend's 9.5 does not")
		assert.Equal(t, int64(9), pro.Tokens.Input)
		assert.Equal(t, []string{"flash", "pro", "friends"}, names(rows))
	}

	js, err := os.ReadFile("../sprintdash/page/app.js")
	require.NoError(t, err)
	page := string(js)
	assert.Contains(t, page, `row.tier === "friends"`)
	assert.Contains(t, page, "return tokenText(row.tokens);")
	html, err := os.ReadFile("../sprintdash/page/index.html")
	require.NoError(t, err)
	assert.Contains(t, string(html), "Cost breakdown")
}

func category(rows []CostCategory, name string) (CostCategory, bool) {
	for _, r := range rows {
		if r.Name == name {
			return r, true
		}
	}
	return CostCategory{}, false
}

func names(rows []CostCategory) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}
