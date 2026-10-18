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
		require.Equal(t, 1, code, errs)
		assert.Contains(t, errs, "CONFIG NOT-APPLIED")
	}
	for _, args := range [][]string{{"tier", "set", "pro", "--routes", "pro-a"}, {"tier", "set", "heavy", "--routes", "heavy-a"}} {
		code, _, errs = h.run(t, append(args, "--file", "case.json")...)
		require.Zero(t, code, errs)
	}
	code, out, errs := h.run(t, "status", "--file", "case.json")
	require.Zero(t, code, errs)
	assert.Contains(t, out, "PROVIDER name=opencode funded=true billing=both balance_usd=238 as_of=2026-10-13 pro=1/7\n")
	warning := "provider=opencode funded=true routes-in-no-tier-array=pro-b,pro-c,pro-d,pro-e,pro-f,pro-g; run: nova-config tier set pro --routes pro-a,pro-b,pro-c,pro-d,pro-e,pro-f,pro-g --actor operator --file case.json"
	assert.Contains(t, out, "CONFIG WARN "+warning+"\n")
	assert.Contains(t, out, "CONFIG WARN tier=heavy holds no route of a funded provider; run: nova-config tier show heavy --file case.json\n")
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
					require.Equal(t, 1, code, errs)
					assert.Contains(t, errs, "CONFIG NOT-APPLIED")
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
				if dry {
					require.Zero(t, code, errs)
				} else {
					require.Equal(t, 1, code, errs)
					assert.Contains(t, errs, "CONFIG NOT-APPLIED")
				}
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
			require.Equal(t, 1, code, errs)
			assert.Contains(t, errs, "CONFIG NOT-APPLIED")
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
			if dry {
				require.Zero(t, code, errs)
			} else {
				require.Equal(t, 1, code, errs)
				assert.Contains(t, errs, "CONFIG NOT-APPLIED")
			}
		})
	}
}

func TestMissingProviderRemedyQuotesItsActor(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.dir = t.TempDir()
	h.env["NOVA_FRIEND"] = "review bot"
	code, _, errs := h.run(t, "migrate", "--file", "case.json")
	require.Zero(t, code, errs)
	code, _, errs = h.run(t, "route", "add", "r", "--tier", "pro", "--provider", "nope", "--model", "m", "--deadline", "60", "--file", "case.json")
	require.Equal(t, 1, code, errs)
	remedy := "nova-config provider add nope --billing metered --funded false --actor 'review bot' --file case.json"
	assert.Contains(t, errs, "; run: "+remedy+"\n")
	command := words(remedy)
	code, _, errs = h.run(t, command[1:]...)
	require.Zero(t, code, errs)
	st, err := config.OpenFile(filepath.Join(h.dir, "case.json"))
	require.NoError(t, err)
	history, err := st.History(context.Background(), config.KindProvider, "nope")
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, "review bot", history[0].Actor)
}

func TestLegacyProviderMigrateRefusalNamesTheSameFile(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.dir = t.TempDir()
	h.env["NOVA_FRIEND"] = "review bot"
	path := filepath.Join(h.dir, "case.json")
	raw := []byte(`{"schema":40,"rows":{"route":{"r":{"name":"r","fields":{"provider":"OpenRouter","tier":"pro","model":"m","deadline":"60"}}}}}`)
	require.NoError(t, os.WriteFile(path, raw, 0600))
	code, _, errs := h.run(t, "migrate", "--file", "case.json")
	require.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "previous/schema40 nova-config build")
	assert.Contains(t, errs, `--provider "OpenRouter"`)
	assert.Contains(t, errs, "nova-config route set r --provider <new> --actor 'review bot' --file case.json")
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, raw, unchanged)
}

func TestStatusWarningRemediesUseTheChosenStore(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"--file", "--pg", "--seat"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			h := loopHarness(t)
			h.dir = t.TempDir()
			h.env["NOVA_FRIEND"] = "review bot"
			delete(h.env, "NOVA_SPRINT_REDIS")
			value := "case.json"
			if flag == "--pg" {
				value = dsn
			}
			if flag == "--seat" {
				value = "seat-a"
			}
			flags := []string{flag, value}
			if flag == "--seat" {
				h.env["XDG_CONFIG_HOME"] = h.dir
				h.env["NOVA_PROVIDER_TEST_PASSWORD"] = "fixture-password"
				profileDir := filepath.Join(h.dir, "nova-config")
				require.NoError(t, os.MkdirAll(profileDir, 0700))
				require.NoError(t, os.WriteFile(filepath.Join(profileDir, "seats.tsv"), []byte("seat-a\t"+dsn+"\tNOVA_PROVIDER_TEST_PASSWORD\n"), 0600))
			} else {
				code, _, errs := h.run(t, append([]string{"migrate"}, flags...)...)
				require.Zero(t, code, errs)
			}
			commands := [][]string{{"provider", "add", "p", "--billing", "metered", "--funded", "true"}, {"route", "add", "a", "--tier", "pro", "--provider", "p", "--model", "m", "--deadline", "60"}, {"route", "add", "b", "--tier", "pro", "--provider", "p", "--model", "m", "--deadline", "60"}, {"tier", "set", "pro", "--routes", "a"}}
			for _, command := range commands {
				code, _, errs := h.run(t, append(command, flags...)...)
				if command[0] == "route" {
					require.Equal(t, 1, code, errs)
					assert.Contains(t, errs, "CONFIG NOT-APPLIED")
				} else {
					require.Zero(t, code, errs)
				}
			}
			code, out, errs := h.run(t, append([]string{"status"}, flags...)...)
			require.Zero(t, code, errs)
			write := "nova-config tier set pro --routes a,b --actor 'review bot' " + flag + " " + value
			show := "nova-config tier show heavy " + flag + " " + value
			assert.Contains(t, out, "; run: "+write+"\n")
			assert.Contains(t, out, "; run: "+show+"\n")
			command := words(write)
			code, _, errs = h.run(t, command[1:]...)
			require.Zero(t, code, errs, "the warning's correction runs on this store")
			code, out, errs = h.run(t, append([]string{"status"}, flags...)...)
			require.Zero(t, code, errs)
			assert.NotContains(t, out, "routes-in-no-tier-array")
		})
	}
}

// The add/set/show/history verbs carry an uncatalogued provider's direct fields, and the
// key is the variable's name only.
func TestProviderDirectFieldsThroughTheVerbs(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.dir = t.TempDir()
	h.env["NOVA_FRIEND"] = "operator"
	code, _, errs := h.run(t, "migrate", "--file", "case.json")
	require.Zero(t, code, errs)
	code, _, errs = h.run(t, "provider", "add", "paperinstruments", "--billing", "metered", "--funded", "true",
		"--base_url", "https://api.example.invalid/v1", "--key_env", "PAPERINSTRUMENTS_API_KEY",
		"--npm", "@ai-sdk/openai-compatible", "--file", "case.json")
	require.Zero(t, code, errs)
	code, out, errs := h.run(t, "provider", "show", "paperinstruments", "--file", "case.json")
	require.Zero(t, code, errs)
	assert.Contains(t, out, "PROVIDER name=paperinstruments")
	assert.Contains(t, out, "base_url=https://api.example.invalid/v1")
	assert.Contains(t, out, "key_env=PAPERINSTRUMENTS_API_KEY")
	assert.Contains(t, out, "npm=@ai-sdk/openai-compatible")
	code, _, errs = h.run(t, "provider", "set", "paperinstruments", "--npm", "@ai-sdk/openai", "--file", "case.json")
	require.Zero(t, code, errs)
	code, out, errs = h.run(t, "provider", "history", "paperinstruments", "--file", "case.json")
	require.Zero(t, code, errs)
	assert.Contains(t, out, "npm=@ai-sdk/openai-compatible>@ai-sdk/openai")
}
