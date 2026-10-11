package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFriendLintValidatesStoredRows tests that nova-config friend lint
// examines every stored friend row, runs each field's write-time validator
// over it, and prints one LINT line per row and field that fails.
// A store with all rows passing prints LINT OK rows=<n>.
func TestFriendLintValidatesStoredRows(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.dir = t.TempDir()

	// Create file store
	fileName := "test.json"
	code, _, errs := h.run(t, "migrate", "--file", fileName)
	require.Equal(t, 0, code, "migrate should succeed: %s", errs)

	// Add a valid friend row (slots must be >= default width 8; roles must name one)
	code, _, errs = h.run(t, "friend", "add", "test", "--slots", "8", "--tiers", "flash", "--roles", "builder", "--file", fileName, "--as", "test")
	require.Equal(t, 0, code, "friend add should succeed: %s", errs)

	code, out, errs := h.run(t, "friend", "lint", "--file", fileName)
	require.Equal(t, 0, code, "lint should succeed when all rows pass: %s %s", out, errs)
	require.Contains(t, out, "LINT OK")
	require.Contains(t, out, "rows=1")
}

// TestFriendLintReportsBatchMode tests that friend lint catches stored rows
// with arbitrary invalid values, but a row the write path would accept is OK.
func TestFriendLintReportsBatchMode(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.dir = t.TempDir()

	// Create file store
	fileName := "test.json"
	code, _, errs := h.run(t, "migrate", "--file", fileName)
	require.Equal(t, 0, code, "migrate should succeed: %s", errs)

	// Add a valid friend row (batch mode is not allowed via add/set)
	code, _, errs = h.run(t, "friend", "add", "test", "--slots", "8", "--tiers", "flash", "--roles", "builder", "--file", fileName, "--as", "test")
	require.Equal(t, 0, code, "friend add should succeed: %s", errs)

	// lint should report LINT OK since no invalid rows exist
	code, out, errs := h.run(t, "friend", "lint", "--file", fileName)
	require.Equal(t, 0, code, "lint should succeed when all rows pass: %s %s", out, errs)
	require.Contains(t, out, "LINT OK")
}

// TestFriendLintEmptyStore tests that lint reports OK with zero rows.
func TestFriendLintEmptyStore(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.dir = t.TempDir()

	// Create file store
	fileName := "empty.json"
	code, _, errs := h.run(t, "migrate", "--file", fileName)
	require.Equal(t, 0, code, "migrate should succeed: %s", errs)

	code, out, errs := h.run(t, "friend", "lint", "--file", fileName)
	require.Equal(t, 0, code, "lint should succeed with empty store: %s %s", out, errs)
	require.Contains(t, out, "LINT OK")
	require.Contains(t, out, "rows=0")
}

// TestFriendLintCatchesBatchMode tests that lint detects a stored row whose
// value the write path refuses, whatever the field: the retired batch mode,
// and an empty list (roles) that names no role at all. Both are written by
// hand to bypass the write-time refusal the way an old or edited store would.
func TestFriendLintCatchesBatchMode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		mode  string
		roles string
		// field and fix are the field and the fix command the LINT line must carry.
		field string
		fix   string
	}{
		{name: "batch mode", mode: "batch", roles: "builder", field: "mode", fix: "--mode one-shot"},
		{name: "empty roles", mode: "one-shot", roles: "", field: "roles", fix: "--roles builder,may-hold,reader"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness()
			h.dir = t.TempDir()

			// One file store with the row written by hand: this simulates a row
			// stored before a write-time refusal, or edited outside the tool.
			fileName := "stored.json"
			data := fmt.Sprintf(`{"schema":1,"rows":{"friend":{"batchy":{"name":"batchy","Fields":{"slots":"8","tiers":"flash","roles":%q,"width":"8","mode":%q,"config_dir":"","token_cap":"6000000","streams":"","kinds":""}}}},"history":[]}`, tc.roles, tc.mode)
			err := os.WriteFile(h.dir+"/"+fileName, []byte(data), 0644)
			require.NoError(t, err)

			code, out, errs := h.run(t, "friend", "lint", "--file", fileName)
			require.Equal(t, 1, code, "lint should fail when %s is stored: %s %s", tc.field, out, errs)
			require.Contains(t, out, "LINT")
			require.Contains(t, out, " "+tc.field+": ")
			require.Contains(t, out, tc.fix, "the LINT line names the fix")
		})
	}
}
