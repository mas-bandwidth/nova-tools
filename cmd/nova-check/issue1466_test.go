package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	if err := os.MkdirAll(receipts, 0o755); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts)
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, stderr)
	}
	if !strings.Contains(stderr, receipts) {
		t.Fatalf("the refusal does not name the receipts path:\n%s", stderr)
	}
	if !strings.Contains(stderr, "--allow-empty") {
		t.Fatalf("the refusal does not name the remedy:\n%s", stderr)
	}
	if got := len(strings.Split(strings.TrimRight(stderr, "\n"), "\n")); got != 1 {
		t.Fatalf("the refusal is %d lines, want 1:\n%s", got, stderr)
	}

	code, stdout, stderr := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts, "--allow-empty")
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "DOGFOOD GATE OK") {
		t.Fatalf("no gate line with --allow-empty:\n%s", stdout)
	}
	if !strings.Contains(stdout, "require-all=no") {
		t.Fatalf("--allow-empty disturbed the old summary:\n%s", stdout)
	}
}
