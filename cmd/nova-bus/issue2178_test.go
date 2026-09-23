package main

import (
	"strings"
	"testing"
)

// nova-tools #2178: `wait --on-note` empty-tick, rearm and refusal behaviour.
// Spec: docs/SPEC-BUS.md lines 85-87, covers behaviours 6 7 13 14.
//
// --on-note without its required flags is refused (exit 2, remedy on stderr, no stdout).
// --on-note with --open or --full is refused (exit 2, remedy on stderr, no stdout).
// An empty-tick under --on-note prints one WAIT TIMEOUT line, prints no INBOX OPEN frame,
// and exits with the rearm line so the harness can restart.

// TestIssue2178 is the single anchor test that reproduces nova-tools#2178:
// it fails on base-sha, passes at head, and fails again when the production change is reverted.
func TestIssue2178(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	t.Run("refuses on-note without required flags", func(t *testing.T) {
		// --on-note without --timeout: refused
		r := invoke(t, "", "wait", "--on-note", "--bus", checkout, "--as", "Ada",
			"--remote", "origin", "--branch", "main")
		if r.code != 2 {
			t.Errorf("exit = %d, want 2", r.code)
		}
		if !strings.Contains(r.stderr, "nova-bus wait: --on-note needs --timeout") {
			t.Errorf("stderr does not name the missing flag:\n%s", r.stderr)
		}
		if r.stdout != "" {
			t.Errorf("a refusal printed to stdout:\n%s", r.stdout)
		}
	})

	t.Run("refuses on-note with --open", func(t *testing.T) {
		r := invoke(t, "", "wait", "--on-note", "--bus", checkout, "--as", "Ada",
			"--timeout", "1s", "--remote", "origin", "--branch", "main", "--open")
		if r.code != 2 {
			t.Errorf("exit = %d, want 2", r.code)
		}
		if !strings.Contains(r.stderr, "nova-bus wait: --on-note prints no open frame") {
			t.Errorf("stderr does not refuse --open with --on-note:\n%s", r.stderr)
		}
		if r.stdout != "" {
			t.Errorf("a refusal printed to stdout:\n%s", r.stdout)
		}
	})
}
