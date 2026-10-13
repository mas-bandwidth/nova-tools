package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusWarnsOnAFundedRouteOutsideEveryArray(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.dir = t.TempDir()
	h.env["NOVA_FRIEND"] = "operator"
	code, _, errs := h.run(t, "migrate", "--file", "case.json")
	require.Zero(t, code, errs)
	for _, args := range [][]string{
		{"provider", "add", "opencode", "--billing", "both", "--funded", "true", "--balance_usd", "238", "--balance_as_of", "2026-10-13"},
		{"provider", "add", "openrouter", "--billing", "metered", "--funded", "false"},
	} {
		code, _, errs = h.run(t, append(args, "--file", "case.json")...)
		require.Zero(t, code, errs)
	}
	for _, name := range []string{"pro-a", "pro-b", "pro-c", "pro-d", "pro-e", "pro-f", "pro-g", "heavy-a"} {
		tier, provider := "pro", "opencode"
		if name == "heavy-a" {
			tier, provider = "heavy", "openrouter"
		}
		code, _, errs = h.run(t, "route", "add", name, "--tier", tier, "--provider", provider, "--model", "m", "--deadline", "60", "--file", "case.json")
		require.Zero(t, code, errs)
	}
	for _, args := range [][]string{{"tier", "set", "pro", "--routes", "pro-a"}, {"tier", "set", "heavy", "--routes", "heavy-a"}} {
		code, _, errs = h.run(t, append(args, "--file", "case.json")...)
		require.Zero(t, code, errs)
	}
	code, out, errs := h.run(t, "status", "--file", "case.json")
	require.Zero(t, code, errs)
	assert.Contains(t, out, "PROVIDER name=opencode funded=true billing=both balance_usd=238 as_of=2026-10-13 pro=1/7\n")
	warning := "provider=opencode funded=true routes-in-no-tier-array=pro-b,pro-c,pro-d,pro-e,pro-f,pro-g; run: nova-config tier set pro --routes pro-a,pro-b,pro-c,pro-d,pro-e,pro-f,pro-g"
	assert.Contains(t, out, "CONFIG WARN "+warning+"\n")
	assert.Contains(t, out, "CONFIG WARN tier=heavy holds no route of a funded provider; run: nova-config tier show heavy\n")
	code, out, errs = h.run(t, "status", "--file", "case.json", "--json")
	require.Zero(t, code, errs)
	var result struct {
		Facts  map[string]json.RawMessage `json:"facts"`
		Notes  []string                   `json:"notes"`
		Result struct {
			Exit int `json:"exit"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Zero(t, result.Result.Exit)
	assert.Contains(t, string(result.Facts["providers"]), "pro=1/7")
	assert.Contains(t, result.Notes, warning)
	code, out, errs = h.run(t, "kinds")
	require.Zero(t, code, errs)
	assert.Contains(t, out, "name=provider table=config.providers")
	assert.Contains(t, out, "rows=many")
}

func addProviders(t *testing.T, st config.Store, names ...string) {
	t.Helper()
	for _, name := range names {
		_, exists, err := st.Get(context.Background(), config.KindProvider, name)
		require.NoError(t, err)
		if exists {
			continue
		}
		k, _ := config.Lookup(config.KindProvider)
		row, err := k.NewRow(name, map[string]string{"billing": "metered", "funded": "false"})
		require.NoError(t, err)
		_, err = st.Insert(context.Background(), config.KindProvider, row, "fixture")
		require.NoError(t, err)
	}
}

const noFundedTierWarnings = "CONFIG WARN tier=flash holds no route of a funded provider; run: nova-config tier show flash\n" +
	"CONFIG WARN tier=heavy holds no route of a funded provider; run: nova-config tier show heavy\n" +
	"CONFIG WARN tier=pro holds no route of a funded provider; run: nova-config tier show pro\n"

func TestMissingProviderRemedyRunsOnTheSameFile(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "set"} {
		for _, dry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dry=%v", verb, dry), func(t *testing.T) {
				t.Parallel()
				h := newHarness()
				h.dir = t.TempDir()
				h.env["NOVA_FRIEND"] = "operator"
				code, _, errs := h.run(t, "migrate", "--file", "case.json")
				require.Zero(t, code, errs)
				if verb == "set" {
					code, _, errs = h.run(t, "provider", "add", "existing", "--billing", "metered", "--funded", "false", "--file", "case.json")
					require.Zero(t, code, errs)
					code, _, errs = h.run(t, "route", "add", "r", "--tier", "pro", "--provider", "existing", "--model", "m", "--deadline", "60", "--file", "case.json")
					require.Zero(t, code, errs)
				}
				args := []string{"route", verb, "r", "--provider", "nope", "--file", "case.json"}
				if verb == "add" {
					args = append(args, "--tier", "pro", "--model", "m", "--deadline", "60")
				}
				if dry {
					args = append(args, "--dry-run")
				}
				code, _, errs = h.run(t, args...)
				require.Equal(t, 1, code, errs)
				assert.Equal(t, 1, strings.Count(errs, "; run: "))
				remedy := "nova-config provider add nope --billing metered --funded false --actor operator --file case.json"
				assert.Contains(t, errs, "; run: "+remedy+"\n")
				command := words(remedy)
				code, _, errs = h.run(t, command[1:]...)
				require.Zero(t, code, errs, "the exact suggested remedy runs")
				code, _, errs = h.run(t, args...)
				require.Zero(t, code, errs)
			})
		}
	}
}

func TestSetRepairsTheUnchangedMissingProvider(t *testing.T) {
	t.Parallel()
	for _, dry := range []bool{false, true} {
		t.Run(fmt.Sprint(dry), func(t *testing.T) {
			t.Parallel()
			h := newHarness()
			h.dir = t.TempDir()
			h.env["NOVA_FRIEND"] = "operator"
			code, _, errs := h.run(t, "migrate", "--file", "case.json")
			require.Zero(t, code, errs)
			code, _, errs = h.run(t, "provider", "add", "nope", "--billing", "metered", "--funded", "false", "--file", "case.json")
			require.Zero(t, code, errs)
			code, _, errs = h.run(t, "route", "add", "r", "--tier", "pro", "--provider", "nope", "--model", "m", "--deadline", "60", "--file", "case.json")
			require.Zero(t, code, errs)
			// A current file deliberately made inconsistent: set must name the
			// unchanged missing reference from the typed store error, not its args.
			path := filepath.Join(h.dir, "case.json")
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			var state map[string]any
			require.NoError(t, json.Unmarshal(raw, &state))
			delete(state["rows"].(map[string]any)["provider"].(map[string]any), "nope")
			raw, err = json.Marshal(state)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, raw, 0600))
			args := []string{"route", "set", "r", "--tokens", "20", "--file", "case.json"}
			if dry {
				args = append(args, "--dry-run")
			}
			code, _, errs = h.run(t, args...)
			require.Equal(t, 1, code, errs)
			remedy := "nova-config provider add nope --billing metered --funded false --actor operator --file case.json"
			assert.Contains(t, errs, "; run: "+remedy+"\n")
			command := words(remedy)
			code, _, errs = h.run(t, command[1:]...)
			require.Zero(t, code, errs)
			code, _, errs = h.run(t, args...)
			require.Zero(t, code, errs)
		})
	}
}
