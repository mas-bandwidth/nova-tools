package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Defect #1466: with an empty receipts directory the gate finds nothing, so
// len(findings) == 0 and it prints DOGFOOD GATE OK and exits 0 — the release
// lane can pass on zero evidence.
//
// The keeper's ruling: the gate refuses an empty receipt set by name, on one
// line, unless --allow-empty is given.
//
// Asserting only the exit code would pass a change that made the verb refuse
// EVERY invocation, so the --allow-empty case below pins today's summary line,
// require-all=no and all.
func TestIssue1466TheDogfoodGateRefusesAnEmptyReceiptSetUnlessAllowEmpty(t *testing.T) {
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
	require.Contains(t, stdout, "require-all=no", "--allow-empty disturbed the old summary:\n%s", stdout)
}
