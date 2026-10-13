package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestARouteNamesAProviderRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := NewMem()
	route, _ := Lookup(KindRoute)
	row, err := route.NewRow("pro-nope", proRoute("nope"))
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindRoute, row, "operator")
	require.ErrorIs(t, err, ErrNoRef)
	assert.Contains(t, err.Error(), "run: nova-config provider add nope --billing metered --funded false")
	provider, ok := Lookup("provider")
	require.True(t, ok)
	p, err := provider.NewRow("nope", map[string]string{"billing": "metered", "funded": "false"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, "provider", p, "operator")
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindRoute, row, "operator")
	require.NoError(t, err)
	_, err = st.Delete(ctx, "provider", "nope", "operator")
	require.ErrorIs(t, err, ErrReferenced)
	_, _, err = st.Update(ctx, KindRoute, row.Name, map[string]string{"provider": "absent"}, "operator")
	require.ErrorIs(t, err, ErrNoRef)
}

// addProvider is a fixture's explicit declaration before it writes a route.
func addProvider(t *testing.T, st Store, name string) {
	t.Helper()
	ctx := context.Background()
	_, exists, err := st.Get(ctx, KindProvider, name)
	require.NoError(t, err)
	if exists {
		return
	}
	k, _ := Lookup(KindProvider)
	row, err := k.NewRow(name, map[string]string{"billing": "metered", "funded": "false"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindProvider, row, "fixture")
	require.NoError(t, err)
}

func providerCase(t *testing.T, array string) (providers, routes, tiers []Row) {
	t.Helper()
	st := NewMem()
	addProvider(t, st, "opencode")
	addProvider(t, st, "openrouter")
	_, _, err := st.Update(context.Background(), KindProvider, "opencode", map[string]string{"funded": "true", "billing": "both", "balance_usd": "238", "balance_as_of": "2026-10-13"}, "operator")
	require.NoError(t, err)
	k, _ := Lookup(KindRoute)
	for _, name := range []string{"pro-a", "pro-b", "pro-c", "pro-d", "pro-e", "pro-f", "pro-g", "heavy-a"} {
		raw := proRoute("opencode")
		if name == "heavy-a" {
			raw["tier"], raw["provider"] = "heavy", "openrouter"
		}
		row, err := k.NewRow(name, raw)
		require.NoError(t, err)
		_, err = st.Insert(context.Background(), KindRoute, row, "operator")
		require.NoError(t, err)
	}
	_, _, err = st.Update(context.Background(), KindTier, "pro", map[string]string{"routes": array}, "operator")
	require.NoError(t, err)
	_, _, err = st.Update(context.Background(), KindTier, "heavy", map[string]string{"routes": "heavy-a"}, "operator")
	require.NoError(t, err)
	providers, err = st.List(context.Background(), KindProvider)
	require.NoError(t, err)
	routes, err = st.List(context.Background(), KindRoute)
	require.NoError(t, err)
	tiers, err = st.List(context.Background(), KindTier)
	require.NoError(t, err)
	return
}

func TestStatusWarnsOnAFundedRouteOutsideEveryArray(t *testing.T) {
	t.Parallel()
	providers, routes, tiers := providerCase(t, "pro-a")
	lines, warns := ProviderReport(providers, routes, tiers)
	assert.Contains(t, lines, "PROVIDER name=opencode funded=true billing=both balance_usd=238 as_of=2026-10-13 pro=1/7")
	assert.Contains(t, warns, "provider=opencode funded=true routes-in-no-tier-array=pro-b,pro-c,pro-d,pro-e,pro-f,pro-g; run: nova-config tier set pro --routes pro-a,pro-b,pro-c,pro-d,pro-e,pro-f,pro-g")
	assert.Contains(t, warns, "tier=heavy holds no route of a funded provider; run: nova-config tier show heavy")
}

func TestAnEmptyTierArrayTakesEveryEnabledRoute(t *testing.T) {
	t.Parallel()
	providers, routes, tiers := providerCase(t, "")
	routes = append(routes, Row{Name: "frontier-a", Fields: map[string]string{"provider": "opencode", "tier": "frontier", "enabled": "true"}}, Row{Name: "disabled", Fields: map[string]string{"provider": "opencode", "tier": "pro", "enabled": "false"}})
	lines, warns := ProviderReport(providers, routes, tiers)
	assert.Contains(t, lines, "PROVIDER name=opencode funded=true billing=both balance_usd=238 as_of=2026-10-13 frontier=1/1 pro=7/7")
	for _, warning := range warns {
		assert.NotContains(t, warning, "routes-in-no-tier-array")
		assert.NotContains(t, warning, "tier=frontier")
	}
}

func TestABalanceCarriesItsDate(t *testing.T) {
	t.Parallel()
	k, _ := Lookup(KindProvider)
	for _, tc := range []struct {
		name string
		raw  map[string]string
		want string
	}{
		{"subscription without numeric prices", map[string]string{"billing": "subscription", "funded": "true"}, ""},
		{"needs funding decision", map[string]string{"billing": "metered"}, "--funded is required"},
		{"needs billing decision", map[string]string{"funded": "false"}, "--billing is required"},
		{"needs date", map[string]string{"billing": "both", "funded": "true", "balance_usd": "238"}, "--balance_as_of"},
		{"bad date", map[string]string{"billing": "both", "funded": "true", "balance_usd": "238", "balance_as_of": "2026-02-30"}, "YYYY-MM-DD"},
		{"bad console", map[string]string{"billing": "metered", "funded": "false", "console_url": "http://example.test"}, "https://"},
		{"dated zero balance", map[string]string{"billing": "both", "funded": "true", "balance_usd": "0.00", "balance_as_of": "2026-10-13", "console_url": "https://example.test"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			row, err := k.NewRow("p", tc.raw)
			if tc.want != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.want)
			} else {
				require.NoError(t, err)
				assert.Equal(t, "0", row.Fields["balance_usd"])
			}
		})
	}
	st := NewMem()
	addProvider(t, st, "p")
	_, _, err := st.Update(context.Background(), KindProvider, "p", map[string]string{"balance_usd": "238"}, "operator")
	require.ErrorIs(t, err, ErrInvalid)
	_, _, err = st.Update(context.Background(), KindProvider, "p", map[string]string{"balance_usd": "238", "balance_as_of": "2026-10-13"}, "operator")
	require.NoError(t, err)
	_, _, err = st.Update(context.Background(), KindProvider, "p", map[string]string{"balance_as_of": ""}, "operator")
	require.ErrorIs(t, err, ErrInvalid)
}

func TestFileStoreSeedsOnlyLegacyProviderFacts(t *testing.T) {
	t.Parallel()
	for _, schema := range []int{40, 41} {
		t.Run(fmt.Sprint(schema), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "legacy.json")
			raw, err := json.Marshal(fileState{Schema: schema, Rows: map[string]map[string]Row{
				KindRoute:    {"r": {Name: "r", Fields: map[string]string{"provider": "opencode", "tier": "pro", "enabled": "true"}}},
				KindProvider: {"declared": {Name: "declared", Fields: map[string]string{"billing": "both", "funded": "true", "note": "owner facts"}}},
			}})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, raw, 0600))
			st, err := OpenFile(path)
			require.NoError(t, err)
			row, found, err := st.Get(context.Background(), KindProvider, "opencode")
			require.NoError(t, err)
			if schema == 40 {
				require.True(t, found)
				assert.Equal(t, "false", row.Fields["funded"])
				assert.Equal(t, "metered", row.Fields["billing"])
				assert.Equal(t, "made by migrate from the routes: set --funded and --billing", row.Fields["note"])
				_, _, _, err = st.Migrate(context.Background())
				require.NoError(t, err)
				reopened, err := OpenFile(path)
				require.NoError(t, err)
				rows, err := reopened.List(context.Background(), KindProvider)
				require.NoError(t, err)
				assert.Len(t, rows, 2)
				revision, err := reopened.Rev(context.Background(), KindProvider)
				require.NoError(t, err)
				assert.Zero(t, revision, "migration does not invent operator history")
			} else {
				assert.False(t, found, "current files are never silently repaired")
			}
			declared, found, err := st.Get(context.Background(), KindProvider, "declared")
			require.NoError(t, err)
			require.True(t, found)
			assert.Equal(t, "true", declared.Fields["funded"])
		})
	}
}

func TestProviderDescriptorAndUnknownFacts(t *testing.T) {
	t.Parallel()
	k, ok := Lookup(KindProvider)
	require.True(t, ok)
	assert.False(t, k.Singleton)
	assert.Equal(t, "providers", k.Table)
	assert.Equal(t, []string{"billing", "funded", "balance_usd", "balance_as_of", "console_url", "note"}, k.FieldNames())
	names := KindNames()
	assert.Equal(t, []string{KindProvider, KindRoute, KindTier}, names[len(names)-3:])
	route, _ := Lookup(KindRoute)
	field, ok := route.Field("provider")
	require.True(t, ok)
	assert.Equal(t, TypeRef, field.Type)
	assert.Equal(t, KindProvider, field.Ref)
	assert.Equal(t, "provider:p", ProviderKey("p"))
	assert.Equal(t, ProvidersKey, hashKinds[KindProvider].set)
	rows := []Row{{Name: "unconfirmed", Fields: map[string]string{"funded": "false", "billing": "metered", "note": "made by migrate from the routes: set --funded and --billing"}}}
	lines, warns := ProviderReport(rows, nil, nil)
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "balance_usd=- as_of=-")
	assert.Contains(t, lines[0], "made")
	assert.Contains(t, lines[0], "note=")
	assert.Empty(t, warns)
}

func TestFileUpgradeRefusesEveryLegacyWordWithoutChangingTheFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "legacy.json")
	state := fileState{Schema: 40, Rows: map[string]map[string]Row{KindRoute: {}}}
	for name, word := range map[string]string{"a": "OpenRouter", "b": "my_provider", "c": "vendor.ai"} {
		state.Rows[KindRoute][name] = Row{Name: name, Fields: map[string]string{"provider": word, "tier": "pro", "model": "m", "deadline": "60"}}
	}
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))
	_, err = OpenFile(path)
	var legacy *ProviderNamesError
	require.ErrorAs(t, err, &legacy)
	assert.Len(t, legacy.Routes, 3)
	for _, want := range []string{"OpenRouter", "my_provider", "vendor.ai", "route set a --provider <new>", "route set b --provider <new>", "route set c --provider <new>"} {
		assert.Contains(t, err.Error(), want)
	}
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, raw, unchanged)
	// Fixture-only correction represents the previous binary's route writes.
	for name, word := range map[string]string{"a": "openrouter", "b": "my-provider", "c": "vendor-ai"} {
		row := state.Rows[KindRoute][name]
		row.Fields["provider"] = word
		state.Rows[KindRoute][name] = row
	}
	raw, err = json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))
	st, err := OpenFile(path)
	require.NoError(t, err)
	_, _, _, err = st.Migrate(context.Background())
	require.NoError(t, err)
	providers, err := st.List(context.Background(), KindProvider)
	require.NoError(t, err)
	require.Len(t, providers, 3)
	for _, p := range providers {
		assert.Equal(t, "false", p.Fields["funded"])
	}
}

func TestFileProviderUpgradeKeepsInvalidRouteNamesRefused(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, key, rowName string }{{"invalid map key", "a;id", "a"}, {"invalid row name", "a", "a;id"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "legacy.json")
			state := fileState{Schema: 40, Rows: map[string]map[string]Row{KindRoute: {tc.key: {Name: tc.rowName, Fields: map[string]string{"provider": "vendor.ai", "tier": "pro", "model": "m", "deadline": "60"}}}}}
			raw, err := json.Marshal(state)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, raw, 0600))
			_, err = OpenFile(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not a row name")
			assert.NotContains(t, err.Error(), "route set")
			var legacy *ProviderNamesError
			assert.False(t, errors.As(err, &legacy), "the original name refusal precedes provider remediation")
			unchanged, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, raw, unchanged)
		})
	}
}
