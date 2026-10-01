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
	assert.Equal(t, "tier,provider,model,tokens,deadline,weight,enabled", strings.Join(k.FieldNames(), ","), "the deal reads exactly these names")
	types := map[string]Type{"tier": TypeEnum, "provider": TypeText, "model": TypeText, "tokens": TypeInt, "deadline": TypeInt, "weight": TypeInt, "enabled": TypeBool}
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
	assert.Equal(t, KindRoute, names[len(names)-1], "routes apply last")

	// The Redis view: route:<name> in the set routes, no derived field; a
	// loop's view keeps its log.
	h := hashKinds[KindRoute]
	assert.Equal(t, "route:r1", h.key("r1"))
	assert.Equal(t, RoutesKey, h.set)
	assert.Nil(t, h.extra, "a route has no derived field")
	assert.Equal(t, []any{"log", LoopLog("l1")}, hashKinds[KindLoop].extra("l1"))
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
			name: "defaults: unmetered, weight 1, enabled",
			raw:  map[string]string{"tier": "flash", "provider": "opencode", "model": "m1", "deadline": "600"},
			want: map[string]string{"tokens": "0", "weight": "1", "enabled": "true"},
		},
		{
			name: "a model holding slashes",
			raw:  map[string]string{"tier": "pro", "provider": "openrouter", "model": "x-ai/grok-4", "deadline": "1800", "weight": "0", "enabled": "false"},
			want: map[string]string{"model": "x-ai/grok-4", "weight": "0", "enabled": "false"},
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
			name: "a bad tier, a slashed provider and a negative weight, all at once",
			raw:  map[string]string{"tier": "max", "provider": "a/b", "model": "m", "deadline": "60", "weight": "-1"},
			errs: []string{`--tier "max"`, `--provider "a/b"`, `--weight "-1": want a non-negative integer`},
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
		{name: "out of the draw by weight", changes: map[string]string{"weight": "0"}},
		{name: "another provider and model", changes: map[string]string{"provider": "openrouter", "model": "x-ai/grok-4"}},
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

	_, _, err = st.Update(ctx, KindRoute, "pro-opencode", map[string]string{"weight": "3"}, "t")
	require.NoError(t, err)
	_, err = st.Delete(ctx, KindRoute, "pro-deepseek", "t")
	require.NoError(t, err)
	_, err = Apply(ctx, st, ap, KindRoute, "t", false, report)
	require.NoError(t, err)
	assert.Equal(t, []string{"APPLY SET kind=route name=pro-opencode changed=weight", "APPLY REMOVE kind=route name=pro-deepseek"}, lines)
	rev, _ = st.Rev(ctx, KindRoute)
	assert.Equal(t, rev, ap.revs[KindRoute])
}
