package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The gate refuses an empty receipt set by name, on one line, unless
// --allow-empty is given: an empty receipts directory has no findings, and an
// OK on zero evidence would let a release pass.
//
// Asserting only the exit code would pass a change that made the verb refuse
// EVERY invocation, so the --allow-empty case below pins the summary line,
// require-all=no and all.
func TestDogfoodGateRefusesAnEmptyReceiptSetUnlessAllowEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	require.NoError(t, os.MkdirAll(receipts, 0o755))

	code, _, stderr := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts)
	require.EqualValues(t, 1, code, "exit %d, want 1\n%s", code, stderr)
	require.Contains(t, stderr, receipts, "the refusal does not name the receipts path:\n%s", stderr)
	require.Contains(t, stderr, "--allow-empty", "the refusal does not name the remedy:\n%s", stderr)
	{
		got := len(strings.Split(strings.TrimRight(stderr, "\n"), "\n"))
		require.EqualValues(t, 1, got, "the refusal is %d lines, want 1:\n%s", got, stderr)
	}

	code, stdout, stderr := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts, "--allow-empty")
	require.EqualValues(t, 0, code, "exit %d, want 0\n%s", code, stderr)
	require.Contains(t, stdout, "DOGFOOD GATE OK", "no gate line with --allow-empty:\n%s", stdout)
	require.Contains(t, stdout, "require-all=no", "--allow-empty changed the summary line:\n%s", stdout)
}
