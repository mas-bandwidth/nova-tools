package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigListMaxFlag(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.dir = t.TempDir()
	var out string
	code, out, stderr := h.run(t, "migrate", "--file", "try.json")
	require.Equal(t, 0, code, "migrate fixture: stdout=%q stderr=%q", out, stderr)
	for _, name := range []string{"a", "b", "c"} {
		code, out, stderr = h.run(t, "machine", "add", name, "--user", "nova", "--seat", "seat", "--slots", "2", "--actor", "tester", "--file", "try.json")
		require.Equal(t, 0, code, "add %s: stdout=%q stderr=%q", name, out, stderr)
	}

	code, all, stderr := h.run(t, "machine", "list", "--file", "try.json")
	require.Equal(t, 0, code, "unbounded: %q", stderr)
	code, zero, stderr := h.run(t, "machine", "list", "--file", "try.json", "--max", "0")
	require.Equal(t, 0, code, "--max 0: %q", stderr)
	assert.Equal(t, all, zero, "zero is the all-items sentinel and leaves legacy output intact")

	code, limited, stderr := h.run(t, "machine", "list", "--file", "try.json", "--max", "1")
	require.Equal(t, 0, code, "--max 1: %q", stderr)
	assert.Contains(t, limited, "MACHINE name=a ")
	assert.NotContains(t, limited, "MACHINE name=b ")
	assert.Contains(t, limited, "CONFIG MORE kind=machine shown=1 total=3")
	assert.Contains(t, limited, "CONFIG LIST kind=machine rows=3")

	code, raw, stderr := h.run(t, "machine", "list", "--file", "try.json", "--max", "1", "--json")
	require.Equal(t, 0, code, "JSON list: %q", stderr)
	var listed struct {
		Facts map[string]any `json:"facts"`
		Items []struct {
			Kind   string         `json:"kind"`
			Fields map[string]any `json:"fields"`
		} `json:"items"`
		More []struct {
			Kind  string `json:"kind"`
			Shown int    `json:"shown"`
			Total int    `json:"total"`
		} `json:"more"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &listed), raw)
	assert.Equal(t, float64(3), listed.Facts["rows"], "total remains available in JSON")
	assert.Len(t, listed.Items, 1)
	assert.Equal(t, "a", listed.Items[0].Fields["name"])
	require.Len(t, listed.More, 1)
	assert.Equal(t, 3, listed.More[0].Total)

	opens := h.opens
	for _, maxValue := range []string{"-1", "x"} {
		code, out, stderr = h.run(t, "machine", "list", "--file", "try.json", "--max", maxValue)
		assert.Equal(t, 2, code, "--max %q exit: stdout=%q stderr=%q", maxValue, out, stderr)
		assert.Contains(t, stderr, "--max", "--max %q refusal: %q", maxValue, stderr)
		assert.Equal(t, opens, h.opens, "invalid --max %q must refuse before opening a store", maxValue)
	}

	// History follows the store's stable ascending change order and reports its
	// unbounded total in both renderings as list does.
	for _, slots := range []string{"3", "4"} {
		code, out, stderr = h.run(t, "machine", "set", "a", "--slots", slots, "--actor", "tester", "--file", "try.json")
		require.Equal(t, 0, code, "set slots %s: stdout=%q stderr=%q", slots, out, stderr)
	}
	code, history, stderr := h.run(t, "machine", "history", "a", "--file", "try.json", "--max", "1")
	require.Equal(t, 0, code, "history: %q", stderr)
	assert.Contains(t, history, "CONFIG CHANGE id=1 ")
	assert.Contains(t, history, "CONFIG MORE kind=change shown=1 total=3")
	assert.Contains(t, history, "CONFIG HISTORY kind=machine name=a changes=3")
	code, historyAll, stderr := h.run(t, "machine", "history", "a", "--file", "try.json")
	require.Equal(t, 0, code, "unbounded history: %q", stderr)
	code, historyZero, stderr := h.run(t, "machine", "history", "a", "--file", "try.json", "--max", "0")
	require.Equal(t, 0, code, "--max 0 history: %q", stderr)
	assert.Equal(t, historyAll, historyZero, "zero preserves unbounded history output")
	code, raw, stderr = h.run(t, "machine", "history", "a", "--file", "try.json", "--max", "1", "--json")
	require.Equal(t, 0, code, "JSON history: %q", stderr)
	var historyJSON struct {
		Facts map[string]any `json:"facts"`
		Items []any          `json:"items"`
		More  []struct {
			Kind  string `json:"kind"`
			Shown int    `json:"shown"`
			Total int    `json:"total"`
		} `json:"more"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &historyJSON), raw)
	assert.Equal(t, float64(3), historyJSON.Facts["changes"])
	assert.Len(t, historyJSON.Items, 1)
	require.Len(t, historyJSON.More, 1)
	assert.Equal(t, 3, historyJSON.More[0].Total)
	opens = h.opens
	for _, maxValue := range []string{"-1", "x"} {
		code, out, stderr = h.run(t, "machine", "history", "a", "--file", "try.json", "--max", maxValue)
		assert.Equal(t, 2, code, "history --max %q exit: stdout=%q stderr=%q", maxValue, out, stderr)
		assert.Contains(t, stderr, "--max", "history --max %q refusal: %q", maxValue, stderr)
		assert.Equal(t, opens, h.opens, "invalid history --max %q must refuse before opening a store", maxValue)
	}
}

func TestConfigInvalidEnumListsOptions(t *testing.T) {
	t.Parallel()

	h := newHarness()
	field := func() config.Field {
		kind, ok := config.Lookup(config.KindProvider)
		require.True(t, ok)
		for _, f := range kind.Fields {
			if f.Name == "billing" {
				return f
			}
		}
		t.Fatal("provider billing descriptor not found")
		return config.Field{}
	}()
	args := []string{"provider", "add", "test", "--billing", "invalid", "--funded", "false", "--actor", "tester", "--file", "try.json"}
	code, out, stderr := h.run(t, args...)
	assert.Equal(t, 2, code, "invalid enum: stdout=%q stderr=%q", out, stderr)
	assert.Empty(t, out)
	for _, option := range field.Enum {
		assert.Contains(t, stderr, option, "plain refusal must list every valid option")
	}
	assert.Contains(t, stderr, "; run: nova-config provider add -h")
	assert.Equal(t, 0, h.opens, "enum validation must refuse before opening the store")

	args = append(args, "--json")
	code, raw, stderr := h.run(t, args...)
	assert.Equal(t, 2, code, "JSON invalid enum: stderr=%q", stderr)
	var refused struct {
		Result struct {
			Exit   int      `json:"exit"`
			Remedy string   `json:"remedy"`
			Why    []string `json:"why"`
		} `json:"result"`
		Facts map[string][]string `json:"facts"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &refused), raw)
	assert.Equal(t, 2, refused.Result.Exit)
	assert.Equal(t, "nova-config provider add -h", refused.Result.Remedy)
	assert.Equal(t, field.Enum, refused.Facts["valid_options"])
	require.Len(t, refused.Result.Why, 1)
	assert.True(t, strings.Contains(refused.Result.Why[0], "valid options:"), refused.Result.Why[0])

	// The command-level kind selector is also an enum and shares the same
	// structured choices, even though it is not a row field descriptor.
	code, raw, stderr = h.run(t, "apply", "--kind", "bogus", "--json")
	assert.Equal(t, 2, code, "invalid apply kind: stderr=%q", stderr)
	require.NoError(t, json.Unmarshal([]byte(raw), &refused), raw)
	assert.Equal(t, config.KindNames(), refused.Facts["valid_options"])

	// A comma-list enum field uses the same descriptor choices, not the list
	// parser's less actionable error by itself.
	friend, ok := config.Lookup(config.KindFriend)
	require.True(t, ok)
	var tiers []string
	for _, f := range friend.Fields {
		if f.Name == "tiers" {
			tiers = f.Enum
		}
	}
	require.NotEmpty(t, tiers)
	code, raw, stderr = h.run(t, "friend", "add", "emma", "--tiers", "bogus", "--actor", "tester", "--file", "try.json", "--json")
	assert.Equal(t, 2, code, "invalid list enum: stderr=%q", stderr)
	require.NoError(t, json.Unmarshal([]byte(raw), &refused), raw)
	assert.Equal(t, tiers, refused.Facts["valid_options"])
}
