package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #1466: the dogfood gate called an empty receipt set OK. A release lane
// that read no receipts at all printed `DOGFOOD GATE OK by-nonauthor=0` and
// exited 0, so it could pass on zero evidence. The keeper's ruling is that the
// gate refuses an empty receipt set by name unless --allow-empty is given. The
// exit code alone will not do: a change that refused EVERY invocation would
// also exit 1, so the refusal must name the receipts path and the remedy on ONE
// line, and --allow-empty must still print the old summary byte for byte.
func TestIssue1466TheDogfoodGateRefusesAnEmptyReceiptSetUnlessAllowEmpty(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	if err := os.MkdirAll(receipts, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("without allow-empty it refuses", func(t *testing.T) {
		code, _, stderr := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts)
		if code != 1 {
			t.Fatalf("exit %d, want 1:\n%s", code, stderr)
		}
		if !strings.Contains(stderr, receipts) || !strings.Contains(stderr, "--allow-empty") {
			t.Fatalf("the refusal does not name the empty receipt set and the remedy:\n%s", stderr)
		}
		if strings.Count(strings.TrimRight(stderr, "\n"), "\n") != 0 {
			t.Fatalf("the refusal is not one line:\n%s", stderr)
		}
	})

	t.Run("with allow-empty it is exactly today", func(t *testing.T) {
		code, stdout, stderr := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts, "--allow-empty")
		if code != 0 {
			t.Fatalf("exit %d, want 0:\n%s", code, stderr)
		}
		if !strings.Contains(stdout, "DOGFOOD GATE OK") || !strings.Contains(stdout, "require-all=no") {
			t.Fatalf("the old summary is gone:\n%s", stdout)
		}
	})
}
