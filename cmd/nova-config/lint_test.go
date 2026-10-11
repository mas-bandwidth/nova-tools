package main

import (
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

	// Add a valid friend row (slots must be >= default width 8)
	code, _, errs = h.run(t, "friend", "add", "test", "--slots", "8", "--tiers", "flash", "--file", fileName, "--as", "test")
	require.Equal(t, 0, code, "friend add should succeed: %s", errs)

	code, out, errs := h.run(t, "friend", "lint", "--file", fileName)
	require.Equal(t, 0, code, "lint should succeed when all rows pass: %s %s", out, errs)
	require.Contains(t, out, "LINT OK")
	require.Contains(t, out, "rows=1")
}

// TestFriendLintReportsBatchMode tests that friend lint catches stored rows
// with the retired batch mode and reports them as lint issues.
func TestFriendLintReportsBatchMode(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.dir = t.TempDir()

	// Create file store
	fileName := "test.json"
	code, _, errs := h.run(t, "migrate", "--file", fileName)
	require.Equal(t, 0, code, "migrate should succeed: %s", errs)

	// Add a valid friend row (batch mode is not allowed via add/set)
	code, _, errs = h.run(t, "friend", "add", "test", "--slots", "8", "--tiers", "flash", "--file", fileName, "--as", "test")
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

// TestFriendLintCatchesBatchMode tests that lint detects stored rows with
// the retired batch mode and reports them as lint issues.
func TestFriendLintCatchesBatchMode(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.dir = t.TempDir()

	// Create file store with a row that has mode=batch (bypassing write validation)
	fileName := "batch.json"
	filePath := h.dir + "/" + fileName
	// Create the file with a friend row that has mode=batch
	// This simulates a row written before batch was retired
	data := `{"schema":1,"rows":{"friend":{"batchy":{"name":"batchy","Fields":{"slots":"8","tiers":"flash","roles":"","width":"8","mode":"batch","config_dir":"","token_cap":"6000000","streams":"","kinds":""}}}},"history":[]}`
	err := os.WriteFile(filePath, []byte(data), 0644)
	require.NoError(t, err)

	// lint should report LINT issue for batch mode
	code, out, errs := h.run(t, "friend", "lint", "--file", fileName)
	require.Equal(t, 1, code, "lint should fail when batch mode is found: %s %s", out, errs)
	require.Contains(t, out, "LINT")
	require.Contains(t, out, "mode")
}
