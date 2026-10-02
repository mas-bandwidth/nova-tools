package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A run that asked for --json gets its refusal as JSON too: one object on stdout
// in internal/tool's shape (result.status refused or failed, the exit, the why
// and the remedy) and nothing on stderr, whether the refusal came before the
// flags parsed, from them, or from the store; a verb with no --json (inventory)
// keeps its line.
func TestARefusalAskedForAsJSONIsOneJSONObject(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "try.json")
	for _, l := range [][]string{{"migrate", "--file", file}, {"machine", "add", "m1", "--user", "u", "--seat", "s", "--slots", "8", "--as", "a1", "--file", file}} {
		r := freshRun(l...)
		require.Equal(t, 0, r.Code, "setup %v: %+v", l, r)
	}
	cases := []struct {
		name   string
		args   []string
		exit   int
		status string
		remedy string
	}{
		{"an unknown verb", []string{"nothing", "--json"}, 2, "refused", "nova-config help"},
		{"a kind with no verb", []string{"machine", "--json"}, 2, "refused", "nova-config machine -h"},
		{"a flag that does not parse", []string{"kinds", "--json", "--bogus"}, 2, "refused", "nova-config kinds -h"},
		{"every missing flag of an add", []string{"machine", "add", "--json", "--file", file}, 2, "refused", "nova-config machine add -h"},
		{"apply without --as", []string{"apply", "--kind", "machine", "--redis", "127.0.0.1:6379", "--file", file, "--json"}, 2, "refused", "nova-config apply -h"},
		{"apply --dry-run without --as", []string{"apply", "--dry-run", "--kind", "machine", "--redis", "127.0.0.1:6379", "--file", file, "--json"}, 2, "refused", "nova-config apply -h"},
		{"an add the store refuses", []string{"machine", "add", "m1", "--user", "u", "--seat", "s", "--slots", "8", "--as", "a1", "--file", file, "--json"}, 1, "failed", "nova-config machine set m1 --<field> <value> --file " + file},
		{"a width of no row", []string{"machine", "width", "nope", "--file", file, "--json"}, 1, "failed", "nova-config machine list --file " + file},
		{"status before migrate", []string{"status", "--file", filepath.Join(dir, "none.json"), "--json"}, 1, "failed", "nova-config migrate --file " + filepath.Join(dir, "none.json")},
		{"machine self --check with no store", []string{"machine", "self", "--check", "--json"}, 3, "refused", "nova-config machine self -h"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := freshRun(c.args...)
			assert.Equal(t, c.exit, r.Code, "%+v", r)
			assert.Empty(t, r.Stderr, "a refusal asked for as JSON printed a line: %+v", r)
			var got struct {
				Result struct {
					Status string   `json:"status"`
					Exit   int      `json:"exit"`
					Remedy string   `json:"remedy"`
					Why    []string `json:"why"`
				} `json:"result"`
			}
			require.NoError(t, json.Unmarshal([]byte(r.Stdout), &got), "stdout is not one JSON object: %+v", r)
			assert.Equal(t, 1, strings.Count(strings.TrimSpace(r.Stdout), "\n")+1, "more than one line: %+v", r)
			assert.Equal(t, c.status, got.Result.Status, "%+v", r)
			assert.Equal(t, c.exit, got.Result.Exit, "%+v", r)
			assert.Equal(t, c.remedy, got.Result.Remedy, "%+v", r)
			assert.NotEmpty(t, got.Result.Why, "%+v", r)
		})
	}
	t.Run("inventory has no --json", func(t *testing.T) {
		t.Parallel()
		r := freshRun("inventory", "--json")
		assert.Equal(t, 2, r.Code, "%+v", r)
		assert.Empty(t, r.Stdout, "%+v", r)
		assert.True(t, strings.HasPrefix(r.Stderr, "nova-config inventory REFUSED: unknown flag --json"), "%+v", r)
	})
}
