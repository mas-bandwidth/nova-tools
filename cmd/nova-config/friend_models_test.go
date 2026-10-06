package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAFriendRowListsHerModelsStrongestFirst: model add, then friend set
// --models a,b; friend show and list print her models, the tiers derived from
// them and her class (the tier of the first); a set of models clears the tiers
// fallback and says so; a model with no row is refused naming model add, and a
// list not strongest first is refused with its reason.
func TestAFriendRowListsHerModelsStrongestFirst(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_FRIEND"] = "rowan"
	step := func(want int, args ...string) (string, string) {
		t.Helper()
		code, out, errs := h.run(t, args...)
		require.Equal(t, want, code, "%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out, errs)
		return out, errs
	}
	step(0, "model", "add", "grok-4-7-xhigh", "--tier", "heavy", "--note", "x-ai Grok 4.7 at xhigh under grok")
	step(0, "model", "add", "grok-4-7", "--tier", "pro")
	out, _ := step(0, "model", "list")
	assert.Contains(t, out, "MODEL name=grok-4-7 tier=pro note=-\n")
	step(0, "friend", "add", "johnny", "--slots", "1", "--tiers", "heavy,pro")
	out, _ = step(0, "friend", "show", "johnny")
	assert.Contains(t, out, " tiers=heavy,pro ", "the fallback: %q", out)
	assert.Contains(t, out, " models=- ", "no models yet: %q", out)
	assert.True(t, strings.HasSuffix(out, " class=heavy\n"), "the fallback's class is its highest tier: %q", out)

	out, _ = step(0, "friend", "set", "johnny", "--models", "grok-4-7-xhigh,grok-4-7")
	assert.Contains(t, out, "CONFIG SET kind=friend name=johnny rev=", "set: %q", out)
	assert.Contains(t, out, "changed=models,tiers", "the fallback cleared with the set: %q", out)
	assert.Contains(t, out, "NOTE friend=johnny tiers=-: her tiers derive from her models now", "said: %q", out)
	out, _ = step(0, "friend", "show", "johnny")
	assert.True(t, strings.HasPrefix(out, "FRIEND name=johnny slots=1 tiers=heavy,pro roles=- "), "the tiers derived from her models: %q", out)
	assert.Contains(t, out, " models=grok-4-7-xhigh,grok-4-7 ", "her models in order: %q", out)
	assert.True(t, strings.HasSuffix(out, " class=heavy\n"), "her class is the tier of her first model: %q", out)
	out, _ = step(0, "friend", "list")
	assert.Contains(t, out, "FRIEND name=johnny slots=1 tiers=heavy,pro ", "list: %q", out)
	assert.Contains(t, out, " models=grok-4-7-xhigh,grok-4-7 class=heavy\n", "list: %q", out)
	out, _ = step(0, "friend", "show", "johnny", "--json")
	var shown struct {
		Items []struct {
			Fields map[string]any `json:"fields"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &shown), "%s", out)
	require.Len(t, shown.Items, 1, "%s", out)
	assert.Equal(t, "heavy", shown.Items[0].Fields["class"], "%s", out)
	assert.Equal(t, "heavy,pro", shown.Items[0].Fields["tiers"], "%s", out)
	assert.Equal(t, "grok-4-7-xhigh,grok-4-7", shown.Items[0].Fields["models"], "%s", out)

	_, errs := step(1, "friend", "set", "johnny", "--models", "grok-5")
	assert.Contains(t, errs, "--models grok-5 names no model row; add it first: nova-config model add grok-5 --tier <flash|pro|heavy|frontier>", "%q", errs)
	_, errs = step(1, "friend", "set", "johnny", "--models", "grok-4-7,grok-4-7-xhigh")
	assert.Contains(t, errs, "want her models strongest to weakest", "%q", errs)
	_, errs = step(1, "model", "remove", "grok-4-7")
	assert.Contains(t, errs, "model grok-4-7 is in the --models of friend johnny", "%q", errs)
}
