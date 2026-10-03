package config

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The route kind (kind.go: Kinds, "route"; docs/SPEC-CONFIG.md, "route"):
// one way to run a model tier, every value data in the row.

// proRoute is the raw of a pro route on provider p.
func proRoute(p string) map[string]string {
	return map[string]string{"tier": "pro", "provider": p, "model": "deepseek-v4", "tokens": "400000", "deadline": "1800"}
}

func TestTheRouteRowIsWhatTheDealReads(t *testing.T) {
	t.Parallel()

	k, ok := Lookup(KindRoute)
	require.True(t, ok)
	assert.Equal(t, "routes", k.Table)
	assert.False(t, k.Singleton)
	assert.Equal(t, "tier,provider,model,tokens,deadline,enabled,"+
		"price_input,price_cache_read,price_cache_write,price_output,reasoning_as_output,long_context,price_input_long,price_output_long,price_request,billing,gateway_percent,price_source,price_as_of,note",
		strings.Join(k.FieldNames(), ","), "the deal reads exactly these names, the card's cost the price sheet after them, and the note last")
	types := map[string]Type{"tier": TypeEnum, "provider": TypeText, "model": TypeText, "tokens": TypeInt, "deadline": TypeInt, "enabled": TypeBool,
		"price_input": TypeDecimal, "price_cache_read": TypeDecimal, "price_cache_write": TypeDecimal, "price_output": TypeDecimal, "reasoning_as_output": TypeBool,
		"long_context": TypeInt, "price_input_long": TypeDecimal, "price_output_long": TypeDecimal, "price_request": TypeDecimal, "billing": TypeEnum,
		"gateway_percent": TypeDecimal, "price_source": TypeText, "price_as_of": TypeText, "note": TypeText}
	required := map[string]bool{"tier": true, "provider": true, "model": true, "deadline": true}
	for _, f := range k.Fields {
		assert.Equal(t, types[f.Name], f.Type, "--%s", f.Name)
		assert.Equal(t, required[f.Name], f.Required, "--%s required", f.Name)
	}
	tier, _ := k.Field("tier")
	assert.Equal(t, []string{"flash", "pro"}, tier.Enum, "frontier cards escalate to the coordinator and are never drawn from routes")
	for _, w := range tier.Enum {
		assert.Contains(t, Tiers, w, "a route tier is a tier")
	}
	names := KindNames()
	assert.Equal(t, []string{KindRoute, KindTier}, names[len(names)-2:], "routes apply before the tiers that name them")

	// The Redis view: route:<name> in the set routes, no derived field; a
	// loop's view keeps its log.
	h := hashKinds[KindRoute]
	assert.Equal(t, "route:r1", h.key("r1"))
	assert.Equal(t, RoutesKey, h.set)
	assert.Nil(t, h.extra, "a route has no derived field")
	assert.NotNil(t, hashKinds[KindLoop].extra, "a loop's view keeps its derived log field")
	assert.Equal(t, LoopsKey, hashKinds[KindLoop].set)
}

func TestRouteNewRowCanonicalisesAndRefusesEveryProblemAtOnce(t *testing.T) {
	t.Parallel()

	k, _ := Lookup(KindRoute)
	cases := []struct {
		name string
		raw  map[string]string
		want map[string]string // the canonical fields asserted, when the row is accepted
		errs []string          // every phrase the one refusal must say
		not  []string          // phrases it must not say
	}{
		{
			name: "defaults: unmetered, enabled",
			raw:  map[string]string{"tier": "flash", "provider": "opencode", "model": "m1", "deadline": "600"},
			want: map[string]string{"tokens": "0", "enabled": "true"},
		},
		{
			name: "a model holding slashes",
			raw:  map[string]string{"tier": "pro", "provider": "openrouter", "model": "x-ai/grok-4", "deadline": "1800", "enabled": "false", "note": "held while the price is read"},
			want: map[string]string{"model": "x-ai/grok-4", "enabled": "false", "note": "held while the price is read"},
		},
		{
			name: "frontier is no route's tier",
			raw:  map[string]string{"tier": "frontier", "provider": "p", "model": "m", "deadline": "60"},
			errs: []string{`--tier "frontier": want one of flash, pro`},
		},
		{
			name: "a provider with a slash",
			raw:  map[string]string{"tier": "pro", "provider": "x-ai/grok", "model": "m", "deadline": "60"},
			errs: []string{`--provider "x-ai/grok"`, "no slash"},
		},
		{
			name: "a provider with a blank",
			raw:  map[string]string{"tier": "pro", "provider": "open code", "model": "m", "deadline": "60"},
			errs: []string{`--provider "open code"`},
		},
		{
			name: "an empty provider",
			raw:  map[string]string{"tier": "pro", "provider": " ", "model": "m", "deadline": "60"},
			errs: []string{`--provider ""`},
		},
		{
			name: "a model with a blank",
			raw:  map[string]string{"tier": "pro", "provider": "p", "model": "deepseek v4", "deadline": "60"},
			errs: []string{`--model "deepseek v4"`},
		},
		{
			name: "deadline 0",
			raw:  map[string]string{"tier": "pro", "provider": "p", "model": "m", "deadline": "0"},
			errs: []string{"--deadline 0; want the seconds a card on it may run, above 0"},
		},
		{
			name: "every required field missing names each once and no rule",
			raw:  map[string]string{"tokens": "5"},
			errs: []string{"--tier is required", "--provider is required", "--model is required", "--deadline is required"},
			not:  []string{"route r1 has"},
		},
		{
			name: "a bad tier, a slashed provider and a negative budget, all at once",
			raw:  map[string]string{"tier": "max", "provider": "a/b", "model": "m", "deadline": "60", "tokens": "-1"},
			errs: []string{`--tier "max"`, `--provider "a/b"`, `--tokens "-1": want a non-negative integer`},
		},
		{
			name: "no price sheet: every price empty, reasoning billed as output, metered",
			raw:  map[string]string{"tier": "flash", "provider": "p", "model": "m", "deadline": "60"},
			want: map[string]string{"price_input": "", "price_output": "", "price_request": "", "gateway_percent": "", "reasoning_as_output": "true",
				"long_context": "0", "billing": "metered", "price_source": "", "price_as_of": ""},
		},
		{
			name: "a whole price sheet, each decimal in its one spelling",
			raw: map[string]string{"tier": "pro", "provider": "p", "model": "m", "deadline": "60", "price_input": "0.30", "price_cache_read": "0.030",
				"price_cache_write": "000.375", "price_output": "1.2", "reasoning_as_output": "false", "long_context": "200000", "price_input_long": "0.60",
				"price_output_long": "2.40", "price_request": "0.0000125", "billing": "plan", "gateway_percent": "5.50", "price_source": "https://example.com/pricing",
				"price_as_of": "2026-10-01"},
			want: map[string]string{"price_input": "0.3", "price_cache_read": "0.03", "price_cache_write": "0.375", "price_output": "1.2", "reasoning_as_output": "false",
				"long_context": "200000", "price_input_long": "0.6", "price_output_long": "2.4", "price_request": "0.0000125", "billing": "plan",
				"gateway_percent": "5.5", "price_source": "https://example.com/pricing", "price_as_of": "2026-10-01"},
		},
		{
			name: "a price with more digits than a float holds stays exact",
			raw:  map[string]string{"tier": "pro", "provider": "p", "model": "m", "deadline": "60", "price_input": "0.10000000000000000000000000001"},
			want: map[string]string{"price_input": "0.10000000000000000000000000001"},
		},
		{
			name: "a negative, an exponent, a comma, an unknown billing and a bad date, all at once",
			raw: map[string]string{"tier": "pro", "provider": "p", "model": "m", "deadline": "60", "price_input": "-1", "price_output": "1e-6",
				"price_cache_read": "1,5", "billing": "monthly", "price_as_of": "10/01/2026"},
			errs: []string{`--price_input "-1": want a non-negative decimal`, `--price_output "1e-6"`, `--price_cache_read "1,5"`,
				`--billing "monthly": want one of metered, plan`, `--price_as_of "10/01/2026"; want the date the prices were read, YYYY-MM-DD`},
		},
		{
			name: "a threshold with no long prices",
			raw:  map[string]string{"tier": "pro", "provider": "p", "model": "m", "deadline": "60", "long_context": "200000", "price_input_long": "0.6"},
			errs: []string{"--long_context 200000 and no --price_output_long; want both long prices with the threshold"},
			not:  []string{"no --price_input_long"},
		},
		{
			name: "long prices with no threshold",
			raw:  map[string]string{"tier": "pro", "provider": "p", "model": "m", "deadline": "60", "price_input_long": "0.6", "price_output_long": "2.4"},
			errs: []string{"--price_input_long 0.6 and no --long_context", "--price_output_long 2.4 and no --long_context"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			row, err := k.NewRow("r1", tc.raw)
			if len(tc.errs) > 0 {
				require.Error(t, err)
				for _, want := range tc.errs {
					assert.Equal(t, 1, strings.Count(err.Error(), want), "%q once in %s", want, err)
				}
				for _, not := range tc.not {
					assert.NotContains(t, err.Error(), not)
				}
				assert.NotContains(t, err.Error(), "\n", "one refusal line")
				return
			}
			require.NoError(t, err)
			for f, want := range tc.want {
				assert.Equal(t, want, row.Fields[f], "--%s", f)
			}
		})
	}
}

func TestRouteSetIsCheckedOnTheRowItWouldLeave(t *testing.T) {
	t.Parallel()

	k, _ := Lookup(KindRoute)
	cases := []struct {
		name    string
		changes map[string]string
		refuse  string // "" when the set is accepted
	}{
		{name: "deadline 0", changes: map[string]string{"deadline": "0"}, refuse: "--deadline 0"},
		{name: "a slashed provider", changes: map[string]string{"provider": "x-ai/grok"}, refuse: "no slash"},
		{name: "an empty model", changes: map[string]string{"model": ""}, refuse: `--model ""`},
		{name: "out of the deal with its reason", changes: map[string]string{"enabled": "false", "note": "4 of 52 ok"}},
		{name: "another provider and model", changes: map[string]string{"provider": "openrouter", "model": "x-ai/grok-4"}},
		{name: "a price sheet", changes: map[string]string{"price_input": "0.27", "price_output": "1.10", "price_as_of": "2026-10-01"}},
		{name: "a threshold with no long prices", changes: map[string]string{"long_context": "128000"}, refuse: "no --price_input_long"},
		{name: "a long price with no threshold", changes: map[string]string{"price_output_long": "2"}, refuse: "--price_output_long 2 and no --long_context"},
		{name: "a price cleared", changes: map[string]string{"price_input": ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			st := NewMem()
			row, err := k.NewRow("r1", proRoute("opencode"))
			require.NoError(t, err)
			_, err = st.Insert(ctx, KindRoute, row, "t")
			require.NoError(t, err)
			changes, err := k.Changes(tc.changes)
			require.NoError(t, err, "each field is valid alone")
			after, _, err := st.Update(ctx, KindRoute, "r1", changes, "t")
			hist, _ := st.History(ctx, KindRoute, "r1")
			if tc.refuse != "" {
				require.ErrorIs(t, err, ErrInvalid, "a refusal (exit 1), not a failure")
				assert.Contains(t, err.Error(), tc.refuse)
				assert.Len(t, hist, 1, "a refused set writes no history")
				return
			}
			require.NoError(t, err)
			for f, v := range changes {
				assert.Equal(t, v, after.Fields[f])
			}
			assert.Len(t, hist, 2, "the set is one history row")
		})
	}
}

func TestApplyWritesRoutesAndReachesParity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	k, _ := Lookup(KindRoute)
	for _, p := range []string{"opencode", "deepseek"} {
		row, err := k.NewRow("pro-"+p, proRoute(p))
		require.NoError(t, err)
		_, err = st.Insert(ctx, KindRoute, row, "t")
		require.NoError(t, err)
	}
	ap := newFake()
	var lines []string
	report := func(op Op) { lines = append(lines, OpLine("APPLY", KindRoute, op)) }

	_, err := Apply(ctx, st, ap, KindRoute, "t", false, report)
	require.NoError(t, err)
	assert.Equal(t, []string{"APPLY ADD kind=route name=pro-deepseek", "APPLY ADD kind=route name=pro-opencode"}, lines)
	rev, _ := st.Rev(ctx, KindRoute)
	assert.Equal(t, rev, ap.revs[KindRoute], "the stamp is the route kind's revision")
	assert.Equal(t, "deepseek", ap.views[KindRoute]["pro-deepseek"]["provider"])

	lines = nil
	res, err := Apply(ctx, st, ap, KindRoute, "t", false, report)
	require.NoError(t, err)
	assert.Empty(t, lines, "a second apply of the same rows writes nothing")
	assert.Zero(t, res.Add+res.Set+res.Remove)

	_, _, err = st.Update(ctx, KindRoute, "pro-opencode", map[string]string{"tokens": "3"}, "t")
	require.NoError(t, err)
	_, err = st.Delete(ctx, KindRoute, "pro-deepseek", "t")
	require.NoError(t, err)
	_, err = Apply(ctx, st, ap, KindRoute, "t", false, report)
	require.NoError(t, err)
	assert.Equal(t, []string{"APPLY SET kind=route name=pro-opencode changed=tokens", "APPLY REMOVE kind=route name=pro-deepseek"}, lines)
	rev, _ = st.Rev(ctx, KindRoute)
	assert.Equal(t, rev, ap.revs[KindRoute])

	// a price sheet set is one SET of exactly the fields that changed, and the view
	// holds each decimal in its one spelling
	lines = nil
	prices, err := k.Changes(map[string]string{"price_input": "0.270", "price_output": "1.10", "price_cache_read": "0.07", "price_as_of": "2026-10-01"})
	require.NoError(t, err)
	_, _, err = st.Update(ctx, KindRoute, "pro-opencode", prices, "t")
	require.NoError(t, err)
	_, err = Apply(ctx, st, ap, KindRoute, "t", false, report)
	require.NoError(t, err)
	assert.Equal(t, []string{"APPLY SET kind=route name=pro-opencode changed=price_input,price_cache_read,price_output,price_as_of"}, lines, "in declaration order")
	view := ap.views[KindRoute]["pro-opencode"]
	assert.Equal(t, "0.27", view["price_input"])
	assert.Equal(t, "1.1", view["price_output"])
	assert.Equal(t, "0.07", view["price_cache_read"])
	assert.Equal(t, "", view["price_cache_write"], "a price not set is empty, never 0")
	assert.Equal(t, "true", view["reasoning_as_output"])
	assert.Equal(t, "metered", view["billing"])
}

// A route a tier's array names is held, as a row a ref names is: removing it
// would leave the deal an array naming a route that is not there, which set
// itself refuses (checkTierRoutes). The remove names the tier; once the array
// lets it go, the route goes.
func TestARouteATierNamesIsHeld(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := NewMem()
	route, _ := Lookup(KindRoute)
	for _, name := range []string{"flash-a", "flash-b"} {
		row, err := route.NewRow(name, map[string]string{"tier": "flash", "provider": "p", "model": "m", "deadline": "60"})
		require.NoError(t, err)
		_, err = st.Insert(ctx, KindRoute, row, "a1")
		require.NoError(t, err)
	}
	_, _, err := st.Update(ctx, KindTier, "flash", map[string]string{"routes": "flash-b,flash-a,flash-b"}, "a1")
	require.NoError(t, err)
	_, err = st.Delete(ctx, KindRoute, "flash-a", "a1")
	assert.ErrorIs(t, err, ErrReferenced)
	assert.ErrorContains(t, err, "route flash-a is in the --routes of tier flash")
	_, _, err = st.Update(ctx, KindTier, "flash", map[string]string{"routes": "flash-b"}, "a1")
	require.NoError(t, err)
	_, err = st.Delete(ctx, KindRoute, "flash-a", "a1")
	assert.NoError(t, err)
}
