//go:build functional

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRouteThroughTheGrammarOnARealStore drives the route kind's verbs
// against a throwaway Postgres and Redis: add, a refused set, apply --kind
// route writing the hash the deal reads, status at parity, remove.
func TestRouteThroughTheGrammarOnARealStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	r := newReal(t, true)
	r.run(t, 0, "migrate")
	out, _ := r.run(t, 0, "route", "add", "pro-grok-openrouter", "--tier", "pro", "--provider", "openrouter", "--model", "x-ai/grok-4", "--tokens", "300000", "--deadline", "1800")
	require.Equal(t, "CONFIG ADD kind=route name=pro-grok-openrouter rev=1\n", out)
	_, errs := r.run(t, 1, "route", "set", "pro-grok-openrouter", "--deadline", "0")
	assert.Equal(t, "nova-config route set: route pro-grok-openrouter has --deadline 0; want the seconds a card on it may run, above 0; run: nova-config route show pro-grok-openrouter\n", errs)
	out, _ = r.run(t, 0, "route", "show", "pro-grok-openrouter")
	assert.True(t, strings.HasPrefix(out, "ROUTE name=pro-grok-openrouter tier=pro provider=openrouter model=x-ai/grok-4 tokens=300000 deadline=1800 enabled=true created="), out)

	out, _ = r.run(t, 0, "apply", "--kind", "route")
	require.True(t, strings.HasPrefix(out, "APPLY ADD kind=route name=pro-grok-openrouter\nCONFIG APPLY kind=route add=1 set=0 remove=0 rev=1 ms="), out)
	h := r.client.HGetAll(ctx, config.RouteKey("pro-grok-openrouter")).Val()
	assert.Equal(t, "openrouter", h["provider"])
	assert.Equal(t, "x-ai/grok-4", h["model"])
	assert.Equal(t, "1", h["rev"])
	assert.True(t, r.client.SIsMember(ctx, config.RoutesKey, "pro-grok-openrouter").Val())
	out, _ = r.run(t, 0, "status")
	assert.Contains(t, out, " route=1 route_rev=1 ")
	assert.True(t, strings.HasSuffix(out, " route_applied=1 tier_applied=0\n"), out)

	r.run(t, 0, "route", "remove", "pro-grok-openrouter")
	out, _ = r.run(t, 0, "apply", "--kind", "route")
	assert.True(t, strings.HasPrefix(out, "APPLY REMOVE kind=route name=pro-grok-openrouter\n"), out)
	assert.Zero(t, r.client.Exists(ctx, config.RouteKey("pro-grok-openrouter")).Val())
}
