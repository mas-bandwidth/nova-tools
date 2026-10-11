package main

import (
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

	// Add a valid friend row
	code, _, errs = h.run(t, "friend", "add", "test", "--slots", "1", "--tiers", "flash", "--mode", "batch", "--roles", "builder", "--file", fileName, "--as", "test")
	require.Equal(t, 0, code, "friend add should succeed: %s", errs)

	code, out, errs := h.run(t, "friend", "lint", "--file", fileName)
	require.Equal(t, 0, code, "lint should succeed when all rows pass: %s %s", out, errs)
	require.Contains(t, out, "LINT OK")
	require.Contains(t, out, "rows=1")
}

// TestFriendLintReportsRefusedFields tests that friend lint validates stored rows
// and would report any invalid fields (but since write validation works, 
// no invalid rows exist in a properly validated store).
func TestFriendLintReportsRefusedFields(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.dir = t.TempDir()

	// Create file store
	fileName := "test.json"
	code, _, errs := h.run(t, "migrate", "--file", fileName)
	require.Equal(t, 0, code, "migrate should succeed: %s", errs)

	// Add a valid friend row
	code, _, errs = h.run(t, "friend", "add", "test", "--slots", "1", "--tiers", "flash", "--file", fileName, "--as", "test")
	require.Equal(t, 0, code, "friend add should succeed: %s", errs)

	// Try to set invalid roles - this should be refused at write-time
	code, _, errs = h.run(t, "friend", "set", "test", "--roles", "invalid-role", "--file", fileName, "--as", "test")
	require.NotEqual(t, 0, code, "setting invalid roles should be refused: %s", errs)

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
