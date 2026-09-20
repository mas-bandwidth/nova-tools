package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/update"
)

// TestApplyShaIsBoundedByTheClock: an injected clock and a fake build sleeping past --timeout
// is exit 2 with the timeout named and no partial --bin directory.
func TestApplyShaIsBoundedByTheClock(t *testing.T) {
	t.Chdir("../..")
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := func(name string, body []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), body, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A fake nova-test binary that sleeps past the timeout
	stub("nova-test", []byte("#!/bin/sh\nsleep 10\n"))
	outPath := filepath.Join(dir, "out.tsv")
	var out, errs strings.Builder
	env := update.Environment{Now: func() time.Time { return time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) }}
	code := update.Run("nova-version", []string{"snapshot", "--bin", bin, "--out", outPath, "--timeout", "100ms"}, "test-stamp", &out, &errs, env)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if !strings.Contains(errs.String(), "timeout") {
		t.Fatalf("stderr does not name timeout:\n%s", errs.String())
	}
	if _, err := os.Stat(outPath); err == nil {
		t.Fatal("partial snapshot file should not be created")
	}
}
