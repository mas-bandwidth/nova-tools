//go:build functional

package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The deal reads the routes nova-config's apply writes (config.RoutesKey, the
// hash config.RouteKey of each, and each tier's array config.TierKey) on a real
// store in two round trips, and takes a pro card's route from them: the hash's
// fields as apply writes them, the array in its order.
func TestRedisTheDealReadsTheRoutesApplyWrites(t *testing.T) {
	t.Parallel()
	st, c := liveStore(t)
	ctx := context.Background()
	require.NoError(t, c.SAdd(ctx, config.RoutesKey, "pro-a", "flash-a").Err())
	require.NoError(t, c.HSet(ctx, config.RouteKey("pro-a"), "name", "pro-a", "tier", "pro", "provider", "openrouter", "model", "x-ai/grok-4",
		"tokens", "400000", "deadline", "1800", "enabled", "true", "rev", "7", "at", "0").Err())
	require.NoError(t, c.HSet(ctx, config.RouteKey("flash-a"), "tier", "flash", "provider", "deepseek", "model", "v4-flash",
		"tokens", "0", "deadline", "900", "enabled", "true").Err())
	require.NoError(t, c.HSet(ctx, config.TierKey("flash"), "name", "flash", "routes", "flash-a,flash-a", "rev", "8", "at", "0").Err())
	require.NoError(t, c.Set(ctx, config.SprintKey(config.FieldDecideJudgment), "0.85", 0).Err())
	set, trips, err := st.B.(RouteReader).Routes(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), trips, "the arrays ride in the routes' second trip")
	assert.Equal(t, "0.85", set.JudgmentBar, "the judgment bar rides with them too (answer --decide reads it from routes --json)")
	assert.Equal(t, map[string][]string{"flash": {"flash-a", "flash-a"}}, set.Tiers, "pro has no array: it takes its routes in name order")
	rs, _, err := st.Routes(ctx)
	require.NoError(t, err)
	require.Len(t, rs, 2)
	want := sprint.Route{Name: "flash-a", Tier: "flash", Provider: "deepseek", Model: "v4-flash", Enabled: true}
	want.Deadline = 900                  // seconds, as the route row holds it
	want.Prices.ReasoningAsOutput = true // an absent price flag uses PricesOf's default
	assert.Equal(t, want, rs[0])
	assert.Equal(t, "x-ai/grok-4", rs[1].Model)

	h := &harness{t: t, st: st, ctx: ctx, now: time.Now(), live: []string{"m1", "m2"}}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 1, Brief: "c: the work (s1) tier: pro\n\nThe task.\n"}))
	h.setPrimary("s1-1", map[string]string{sprint.FieldTierNow: "pro"}) // a pro card on pro (flash first: escalated)
	h.must(DealStep(sprint.DealReq{}))
	wc := h.snap().Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Equal(t, "pro-a", wc.F(sprint.FieldRoute))
	assert.Equal(t, "openrouter/x-ai/grok-4", wc.F(sprint.FieldModel))
	assert.Equal(t, "400000", wc.F(sprint.FieldTokens))
	assert.Equal(t, "1800", wc.F(sprint.FieldDeadline))
}

// The owner's store, 2026-10-01, by the current code: five routes applied as
// nova-config writes them (three flash, two pro), the machine running, then a fresh
// card whose line 1 names flash added and dealt by the tick (not by the deal verb,
// not by a rework): it draws a flash route; and a pro card on pro (escalated: flash
// first) while no pro route is enabled stays ready, held by its tier's one judgment.
func TestRedisTheTickDealsAFreshCardOnARouteOfItsTier(t *testing.T) {
	t.Parallel()
	st, c := liveStore(t)
	ctx := context.Background()
	h := &harness{t: t, st: st, ctx: ctx, now: time.Now(), live: []string{"m1", "m2"}}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.beat()
	put := func(name, tier, enabled string) {
		require.NoError(t, c.SAdd(ctx, config.RoutesKey, name).Err())
		require.NoError(t, c.HSet(ctx, config.RouteKey(name), "name", name, "tier", tier, "provider", "p-"+name, "model", "m",
			"tokens", "100000", "deadline", "900", "enabled", enabled).Err())
	}
	for _, n := range []string{"flash-a", "flash-b", "flash-c"} {
		put(n, "flash", "true")
	}
	put("pro-a", "pro", "false")
	put("pro-b", "pro", "false")
	// a pro card on pro (escalated: flash first), written before the machine runs, while
	// the work table takes a step's write at once
	h.must(AddStep(sprint.AddReq{Stream: "tools", IDs: []string{"pro-card"}, Brief: "pro-card: the work (tools) tier: pro\n\nThe task.\n"}))
	h.setPrimary("pro-card", map[string]string{sprint.FieldTierNow: "pro"})
	h.startMachine()
	h.machine()
	h.must(AddStep(sprint.AddReq{Stream: "testify", IDs: []string{"testify-docs"},
		Brief: "testify-docs: internal/docs tests to testify by the PR 4926 recipe (testify) tier: flash\nBASE: sprint/foundation\n\nThe task.\n"}))
	for i := 0; i < 3; i++ {
		h.tick(time.Second)
		h.machine()
	}
	s := h.snap()
	wc := s.Fleet.Card("testify-docs.w1")
	require.NotNil(t, wc, "the fresh flash card is dealt: it is %s, m1 is %s", s.Work.Card("testify-docs").Col, s.MemberCtl("m1").F("status"))
	assert.Contains(t, []string{"flash-a", "flash-b", "flash-c"}, wc.F(sprint.FieldRoute))
	assert.Equal(t, "p-"+wc.F(sprint.FieldRoute)+"/m", wc.F(sprint.FieldModel))
	assert.Nil(t, s.Fleet.Card("pro-card.w1"), "no enabled pro route: not dealt")
	assert.Equal(t, sprint.Ready, s.Work.Card("pro-card").Col)
	v, err := st.Inbox(ctx, time.Hour, time.Hour, 1000)
	require.NoError(t, err)
	n := 0
	for _, o := range v.Open {
		if o.Note.Type == sprint.NNoRoute {
			n++
			assert.Equal(t, sprint.StreamSubject(sprint.TierSubject("pro")), o.Subject())
		}
	}
	assert.Equal(t, 1, n, "one judgment for the tier")
}
