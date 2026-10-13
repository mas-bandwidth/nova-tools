package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfigInvalidEnumListsOptions pins card config-invalid-enum-lists-valid-options:
// an invalid enum value refuses at exit 2 naming every valid option in plain
// text, and under --json carries them as a structured valid_options array.
func TestConfigInvalidEnumListsOptions(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.dir = t.TempDir()
	h.env["NOVA_FRIEND"] = "operator"
	code, _, errs := h.run(t, "migrate", "--file", "case.json")
	require.Zero(t, code, errs)

	args := []string{"provider", "add", "p1", "--billing", "foo", "--funded", "false", "--file", "case.json"}
	code, out, errs := h.run(t, args...)
	assert.Equal(t, 2, code, "stdout=%q stderr=%q", out, errs)
	assert.Contains(t, errs, "metered")
	assert.Contains(t, errs, "plan")
	assert.Contains(t, errs, "both")

	code, out, errs = h.run(t, append(append([]string{}, args...), "--json")...)
	assert.Equal(t, 2, code, "stdout=%q stderr=%q", out, errs)
	assert.Empty(t, strings.TrimSpace(errs), "refusal is the JSON object, not stderr")
	var obj struct {
		Result struct {
			Status string   `json:"status"`
			Exit   int      `json:"exit"`
			Why    []string `json:"why"`
		} `json:"result"`
		Facts struct {
			ValidOptions []string `json:"valid_options"`
		} `json:"facts"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &obj), out)
	assert.Equal(t, "refused", obj.Result.Status)
	assert.Equal(t, 2, obj.Result.Exit)
	assert.Equal(t, []string{"metered", "plan", "both"}, obj.Facts.ValidOptions, out)
	require.NotEmpty(t, obj.Result.Why)
	assert.Contains(t, obj.Result.Why[0], "metered")
}
