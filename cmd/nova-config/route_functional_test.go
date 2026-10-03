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
	require.Equal(t, "ROUTE-ADD OK op=add kind=route name=pro-grok-openrouter rev=1\n", out)
	_, errs := r.run(t, 1, "route", "set", "pro-grok-openrouter", "--deadline", "0")
	assert.Equal(t, "ROUTE-SET FAILED: route pro-grok-openrouter has --deadline 0; want the seconds a card on it may run, above 0; run: nova-config route show pro-grok-openrouter\n", errs)
	out, _ = r.run(t, 0, "route", "show", "pro-grok-openrouter")
	assert.True(t, strings.HasPrefix(out, "ROUTE name=pro-grok-openrouter tier=pro provider=openrouter model=x-ai/grok-4 tokens=300000 deadline=1800 enabled=true"+noPrices+" created="), out)
	// the price sheet through Postgres's columns (0009): each decimal kept exactly, as text
	out, _ = r.run(t, 0, "route", "set", "pro-grok-openrouter", "--price_input", "3.000", "--price_output", "0.10000000000000000000000000001",
		"--long_context", "128000", "--price_input_long", "6", "--price_output_long", "30", "--billing", "plan", "--price_as_of", "2026-10-01")
	require.Equal(t, "ROUTE-SET OK op=set kind=route name=pro-grok-openrouter rev=2 changed=billing,long_context,price_as_of,price_input,price_input_long,price_output,price_output_long\n", out)

	out, _ = r.run(t, 0, "apply", "--kind", "route")
	require.True(t, strings.HasPrefix(out, "APPLY OK\nAPPLY ADD kind=route name=pro-grok-openrouter\nAPPLY KIND kind=route add=1 set=0 remove=0 rev=2 ms="), out)
	h := r.client.HGetAll(ctx, config.RouteKey("pro-grok-openrouter")).Val()
	assert.Equal(t, "openrouter", h["provider"])
	assert.Equal(t, "x-ai/grok-4", h["model"])
	assert.Equal(t, "2", h["rev"])
	assert.Equal(t, "3", h["price_input"])
	assert.Equal(t, "0.10000000000000000000000000001", h["price_output"])
	assert.Equal(t, "", h["price_cache_read"])
	assert.Equal(t, "128000", h["long_context"])
	assert.Equal(t, "plan", h["billing"])
	assert.Equal(t, "true", h["reasoning_as_output"])
	assert.True(t, r.client.SIsMember(ctx, config.RoutesKey, "pro-grok-openrouter").Val())
	out, _ = r.run(t, 0, "status")
	assert.Contains(t, out, " route=1 route_rev=2 ")
	assert.True(t, strings.HasSuffix(out, " route_applied=2 tier_applied=0\n"), out)

	r.run(t, 0, "route", "remove", "pro-grok-openrouter")
	out, _ = r.run(t, 0, "apply", "--kind", "route")
	assert.True(t, strings.HasPrefix(out, "APPLY OK\nAPPLY REMOVE kind=route name=pro-grok-openrouter\n"), out)
	assert.Zero(t, r.client.Exists(ctx, config.RouteKey("pro-grok-openrouter")).Val())
}
