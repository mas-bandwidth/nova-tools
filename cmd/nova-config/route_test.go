package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The route kind through the one grammar (docs/SPEC-CONFIG.md, "route"),
// on the three pro routes of docs/nova-config/README.md.

// noPrices is a route line's price sheet when none is set: every price a dash,
// reasoning billed as output, no threshold, metered.
const noPrices = " price_input=- price_cache_read=- price_cache_write=- price_output=- reasoning_as_output=true long_context=0 price_input_long=- " +
	"price_output_long=- price_request=- billing=metered gateway_percent=- price_source=- price_as_of=-"

func TestRouteVerbsEndToEndOnTheFake(t *testing.T) {
	t.Parallel()

	h := loopHarness(t)
	steps := []struct {
		name string
		args []string
		code int
		out  string // the whole stdout, or a prefix when pre is set
		errs string // a phrase stderr must hold
		pre  bool
	}{
		{
			name: "add a pro route through one provider",
			args: []string{"route", "add", "pro-deepseek-opencode", "--tier", "pro", "--provider", "opencode", "--model", "deepseek-v4", "--tokens", "400000", "--deadline", "1800"},
			out:  "CONFIG ADD kind=route name=pro-deepseek-opencode rev=1\n",
		},
		{
			name: "add the same model direct",
			args: []string{"route", "add", "pro-deepseek-direct", "--tier", "pro", "--provider", "deepseek", "--model", "deepseek-v4", "--tokens", "400000", "--deadline", "1800"},
			out:  "CONFIG ADD kind=route name=pro-deepseek-direct rev=2\n",
		},
		{
			name: "add a model whose name holds a slash",
			args: []string{"route", "add", "pro-grok-openrouter", "--tier", "pro", "--provider", "openrouter", "--model", "x-ai/grok-4", "--tokens", "300000", "--deadline", "1800"},
			out:  "CONFIG ADD kind=route name=pro-grok-openrouter rev=3\n",
		},
		{
			name: "list prints every field in declaration order, a price not set as a dash",
			args: []string{"route", "list"},
			out: "ROUTE name=pro-deepseek-direct tier=pro provider=deepseek model=deepseek-v4 tokens=400000 usd=- deadline=1800 enabled=true" + noPrices + "\n" +
				"ROUTE name=pro-deepseek-opencode tier=pro provider=opencode model=deepseek-v4 tokens=400000 usd=- deadline=1800 enabled=true" + noPrices + "\n" +
				"ROUTE name=pro-grok-openrouter tier=pro provider=openrouter model=x-ai/grok-4 tokens=300000 usd=- deadline=1800 enabled=true" + noPrices + "\n" +
				"CONFIG LIST kind=route rows=3\n",
		},
		{
			name: "show carries the stamps",
			args: []string{"route", "show", "pro-grok-openrouter"},
			out:  "ROUTE name=pro-grok-openrouter tier=pro provider=openrouter model=x-ai/grok-4 tokens=300000 usd=- deadline=1800 enabled=true" + noPrices + " created=",
			pre:  true,
		},
		{
			name: "set the price sheet, each decimal in its one spelling",
			args: []string{"route", "set", "pro-deepseek-opencode", "--price_input", "0.270", "--price_cache_read", "0.07", "--price_output", "1.10",
				"--long_context", "128000", "--price_input_long", "0.54", "--price_output_long", "2.2", "--gateway_percent", "5", "--price_source", "https://example.com/pricing",
				"--price_as_of", "2026-10-01"},
			out: "CONFIG SET kind=route name=pro-deepseek-opencode rev=4 changed=gateway_percent,long_context,price_as_of,price_cache_read,price_input,price_input_long,price_output,price_output_long,price_source\n",
		},
		{
			name: "show prints the sheet",
			args: []string{"route", "show", "pro-deepseek-opencode"},
			out: "ROUTE name=pro-deepseek-opencode tier=pro provider=opencode model=deepseek-v4 tokens=400000 usd=- deadline=1800 enabled=true price_input=0.27 price_cache_read=0.07 " +
				"price_cache_write=- price_output=1.1 reasoning_as_output=true long_context=128000 price_input_long=0.54 price_output_long=2.2 price_request=- billing=metered " +
				"gateway_percent=5 price_source=https://example.com/pricing price_as_of=2026-10-01 created=",
			pre: true,
		},
		{
			name: "a price that is not a decimal is refused before any store",
			args: []string{"route", "set", "pro-deepseek-opencode", "--price_output", "1.1e0"},
			code: 2,
			errs: `--price_output "1.1e0": want a non-negative decimal`,
		},
		{
			name: "a set that leaves long prices with no threshold is refused",
			args: []string{"route", "set", "pro-deepseek-opencode", "--long_context", "0"},
			code: 1,
			errs: "--price_input_long 0.54 and no --long_context",
		},
		{
			name: "a set that leaves deadline 0 is refused",
			args: []string{"route", "set", "pro-grok-openrouter", "--deadline", "0"},
			code: 1,
			errs: "route pro-grok-openrouter has --deadline 0; want the seconds a card on it may run, above 0; run: nova-config route show pro-grok-openrouter",
		},
		{
			name: "set takes a route out of the deal",
			args: []string{"route", "set", "pro-grok-openrouter", "--enabled", "false"},
			out:  "CONFIG SET kind=route name=pro-grok-openrouter rev=5 changed=enabled\n",
		},
		{
			name: "history names every change",
			args: []string{"route", "history", "pro-grok-openrouter"},
			out:  "HISTORY id=3 kind=route name=pro-grok-openrouter op=add actor=a1 ",
			pre:  true,
		},
		{
			name: "remove",
			args: []string{"route", "remove", "pro-deepseek-direct"},
			out:  "CONFIG REMOVE kind=route name=pro-deepseek-direct rev=6\n",
		},
	}
	for _, s := range steps {
		code, out, errs := h.run(t, s.args...)
		require.Equal(t, s.code, code, "%s: %v\nstdout: %s\nstderr: %s", s.name, s.args, out, errs)
		if s.pre {
			assert.True(t, strings.HasPrefix(out, s.out), "%s: %q, want the prefix %q", s.name, out, s.out)
		} else if s.code == 0 {
			assert.Equal(t, s.out, out, s.name)
		}
		if s.errs != "" {
			assert.Contains(t, errs, s.errs, s.name)
			assert.Equal(t, 1, strings.Count(errs, "\n"), "%s: one refusal line", s.name)
		}
	}
}

func TestRouteUsageRefusalsOpenNoStore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		errs []string
	}{
		{
			name: "frontier, a slashed provider and deadline 0, all at once",
			args: []string{"route", "add", "r1", "--tier", "frontier", "--provider", "x-ai/grok", "--model", "m", "--deadline", "0"},
			errs: []string{`--tier "frontier": want one of flash, pro`, `--provider "x-ai/grok"`, "--deadline 0"},
		},
		{
			name: "every required field missing",
			args: []string{"route", "add", "r1"},
			errs: []string{"--tier is required", "--provider is required", "--model is required", "--deadline is required"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := loopHarness(t)
			code, out, errs := h.run(t, tc.args...)
			assert.Equal(t, 2, code, errs)
			assert.Empty(t, out)
			for _, want := range tc.errs {
				assert.Contains(t, errs, want)
			}
			assert.Equal(t, 0, h.opens, "a usage refusal opened the store")
		})
	}
}

func TestApplyKindRouteWritesTheViewAndStatusShowsParity(t *testing.T) {
	t.Parallel()

	h := loopHarness(t)
	code, _, errs := h.run(t, "route", "add", "r1", "--tier", "flash", "--provider", "p", "--model", "m", "--deadline", "60")
	require.Equal(t, 0, code, errs)

	code, out, _ := h.run(t, "apply", "--kind", "route", "--check")
	require.Equal(t, 0, code)
	assert.Equal(t, "CHECK ADD kind=route name=r1\nCONFIG CHECK kind=route add=1 set=0 remove=0 rev=1 applied=0\n", out)
	assert.Empty(t, h.redis.log, "--check writes nothing")

	code, out, _ = h.run(t, "apply", "--kind", "route")
	require.Equal(t, 0, code)
	assert.Equal(t, "APPLY ADD kind=route name=r1\nCONFIG APPLY kind=route add=1 set=0 remove=0 rev=1 ms=0\n", out)
	assert.Equal(t, []string{"write route r1"}, h.redis.log)
	assert.Equal(t, "true", h.redis.views["route"]["r1"]["enabled"])

	code, out, errs = h.run(t, "status")
	assert.Equal(t, 0, code, errs)
	assert.Contains(t, out, " route=1 route_rev=1 ")
	assert.True(t, strings.HasSuffix(out, " route_applied=1 tier_applied=0\n"), out)

	// the price sheet reaches the view the sprint reads, exactly as typed in its one spelling
	code, _, errs = h.run(t, "route", "set", "r1", "--price_input", "0.10000000000000000000000000001", "--price_output", "2.50", "--billing", "plan")
	require.Equal(t, 0, code, errs)
	code, out, _ = h.run(t, "apply", "--kind", "route")
	require.Equal(t, 0, code)
	assert.Equal(t, "APPLY SET kind=route name=r1 changed=price_input,price_output,billing\nCONFIG APPLY kind=route add=0 set=1 remove=0 rev=2 ms=0\n", out)
	view := h.redis.views["route"]["r1"]
	assert.Equal(t, "0.10000000000000000000000000001", view["price_input"], "a decimal, never through a float")
	assert.Equal(t, "2.5", view["price_output"])
	assert.Equal(t, "plan", view["billing"])
	assert.Equal(t, "", view["price_cache_read"], "a price not set is empty, never 0")
}

// The tier kind through the one grammar: migrate made the flash and pro rows, so
// set writes a tier's route array on a new store, in order and with a name
// repeated; a route that is no row or is disabled is refused; remove is refused
// for both tiers (the deal reads them); list prints both tiers; apply writes
// tier:<name> (docs/SPEC-CONFIG.md, "tier").
func TestTierVerbsEndToEndOnTheFake(t *testing.T) {
	t.Parallel()

	h := loopHarness(t)
	for _, args := range [][]string{
		{"route", "add", "a", "--tier", "flash", "--provider", "p", "--model", "m", "--deadline", "60"},
		{"route", "add", "b", "--tier", "flash", "--provider", "p", "--model", "m", "--deadline", "60"},
		{"route", "add", "off", "--tier", "flash", "--provider", "p", "--model", "m", "--deadline", "60", "--enabled", "false"},
	} {
		code, _, errs := h.run(t, args...)
		require.Equal(t, 0, code, errs)
	}
	code, out, errs := h.run(t, "tier", "set", "flash", "--routes", "a,b,b")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, "CONFIG SET kind=tier name=flash rev=4 changed=routes\n", out)
	for routes, refusal := range map[string]string{"a,nope": "--routes nope names no route row", "off": "--routes off names a disabled route"} {
		code, _, errs = h.run(t, "tier", "set", "flash", "--routes", routes)
		assert.Equal(t, 1, code, routes)
		assert.Contains(t, errs, refusal)
	}
	for _, tier := range []string{"flash", "pro"} {
		code, _, errs = h.run(t, "tier", "remove", tier)
		assert.Equal(t, 1, code, "tier remove %s", tier)
		assert.Contains(t, errs, "tier "+tier+" is made by migrate and the deal reads it; it is never removed")
	}
	code, out, _ = h.run(t, "tier", "list")
	require.Equal(t, 0, code)
	assert.Equal(t, "TIER name=flash routes=a,b,b\nTIER name=pro routes=-\nCONFIG LIST kind=tier rows=2\n", out)
	code, _, errs = h.run(t, "apply", "--kind", "tier")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, "a,b,b", h.redis.views["tier"]["flash"]["routes"])
}
