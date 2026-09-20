package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCutKindRebaseIsReachableFromTheCommandLine closes the gap Emma's #1855 note found
// from the outside: `nova-pulse help cut` did not offer `rebase` among the kinds. It was
// not offered because it could not be used -- internal/pulse declares the kind and
// REQUIRES --branch and --base of it (cutkind.go's cutKindProblem), and cmd/nova-pulse
// defined neither flag, so every `cut --kind rebase` refused whatever the caller typed.
// Advertising the kind in the synopsis without this would be a document promising a verb
// form that cannot run.
func TestCutKindRebaseIsReachableFromTheCommandLine(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "cards")
	queue := filepath.Join(dir, "queue")
	var stdout, stderr bytes.Buffer
	code := run([]string{"cut", "--kind", "rebase", "--repo", "mas-bandwidth/nova-tools",
		"--pr", "1730", "--branch", "rowan/toolwork-t04-selftest", "--base", "dev",
		"--title", "T04 accept gate selftest", "--out", out, "--queue", queue},
		&stdout, &stderr, time.Now().UTC())
	if code != 0 {
		t.Fatalf("cut --kind rebase exited %d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 1 {
		t.Fatalf("want one card under %s: %v %v", out, entries, err)
	}
	raw, err := os.ReadFile(filepath.Join(out, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	line1 := strings.SplitN(string(raw), "\n", 2)[0]
	// The kind's own line-1 shape (cutkind.go's table): it names the PR, the base it is
	// replayed onto and the title, so the harvest can match the card by its contract.
	for _, want := range []string{"RESULT: CARD-", "PR #1730", "rebased onto dev", "T04 accept gate selftest"} {
		if !strings.Contains(line1, want) {
			t.Fatalf("line 1 = %q, want it to hold %q", line1, want)
		}
	}
}
