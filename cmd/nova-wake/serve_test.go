package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// Test 10, the half that says what a person gets wrong: "--redeliver without
// --on-note is refused naming the flag" -- because the state stores no command,
// so a redelivery names its handler.
func TestServeRefusesARedeliveryThatNamesNoHandler(t *testing.T) {
	t.Parallel()

	r := wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan",
		"--state", filepath.Join(t.TempDir(), "s"), "--redeliver", "aaa111")
	if r.exit != 2 {
		t.Fatalf("exit = %d, want 2:\n%s", r.exit, r.all())
	}
	if !strings.Contains(r.stderr, "--on-note") {
		t.Errorf("the refusal does not name the flag it wants:\n%s", r.stderr)
	}
}

// A whitespace-only --on-note is refused as missing rather than accepted and
// panicking on spawn's fields[0] indexing.
func TestServeRefusesWhitespaceOnlyOnNote(t *testing.T) {
	t.Parallel()
	r := wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", "   ",
		"--interval", "30s", "--state", filepath.Join(t.TempDir(), "serve.state"), "--hours", "0.02")
	if r.exit != 2 {
		t.Fatalf("exit = %d, want 2", r.exit)
	}
	if !strings.Contains(r.stderr, "--on-note is required; refusing to guess") {
		t.Fatalf("stderr does not name missing --on-note:\n%s", r.stderr)
	}
	if !strings.Contains(r.stderr, onNoteHint) {
		t.Fatalf("stderr does not include onNoteHint:\n%s", r.stderr)
	}
}
