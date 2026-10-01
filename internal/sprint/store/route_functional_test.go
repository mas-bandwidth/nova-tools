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
// hash config.RouteKey of each) on a real store, and draws a pro card's route
// from them: the hash's fields as apply writes them, a weight left out read as 1.
func TestRedisTheDealReadsTheRoutesApplyWrites(t *testing.T) {
	t.Parallel()
	st, c := liveStore(t)
	ctx := context.Background()
	require.NoError(t, c.SAdd(ctx, config.RoutesKey, "pro-a", "flash-a").Err())
	require.NoError(t, c.HSet(ctx, config.RouteKey("pro-a"), "name", "pro-a", "tier", "pro", "provider", "openrouter", "model", "x-ai/grok-4",
		"tokens", "400000", "deadline", "1800", "weight", "1", "enabled", "true", "rev", "7", "at", "0").Err())
	require.NoError(t, c.HSet(ctx, config.RouteKey("flash-a"), "tier", "flash", "provider", "deepseek", "model", "v4-flash",
		"tokens", "0", "deadline", "900", "enabled", "true").Err())
	rs, err := st.Routes(ctx)
	require.NoError(t, err)
	require.Len(t, rs, 2)
	want := sprint.Route{Name: "flash-a", Tier: "flash", Provider: "deepseek", Model: "v4-flash", Weight: 1, Enabled: true}
	want.Deadline = 900 // seconds, as the route row holds it
	assert.Equal(t, want, rs[0])
	assert.Equal(t, "x-ai/grok-4", rs[1].Model)

	h := &harness{t: t, st: st, ctx: ctx, now: time.Now(), live: []string{"m1", "m2"}}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 1, Brief: "c: the work (s1) tier: pro\n\nThe task.\n"}))
	h.must(DealStep(sprint.DealReq{}))
	wc := h.snap().Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Equal(t, "pro-a", wc.F(sprint.FieldRoute))
	assert.Equal(t, "openrouter/x-ai/grok-4", wc.F(sprint.FieldModel))
	assert.Equal(t, "400000", wc.F(sprint.FieldTokens))
	assert.Equal(t, "1800", wc.F(sprint.FieldDeadline))
}
