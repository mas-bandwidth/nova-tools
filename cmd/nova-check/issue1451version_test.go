package main

import (
	"strings"
	"testing"
)

// Defect #1451, the site PR #1755 disclosed as owed: PR #1755 fixed every
// refusal in this package but `version.go` was outside its PATHS, so the one
// invocation that reaches cmdVersion with a stray argument still refuses at
// exit 2 without naming the door. ONBOARDING.md point 1 asks every exit-2 line
// to end in `; run: nova-check help`.
//
// issue1451_test.go parses the BARE verbs out of the help banner and skips the
// one bare verb that exits 0 (version); it never invokes `version` with an
// argument, so this path is not in its table and needs its own assertion.
func TestVersionsStrayArgumentRefusalNamesTheDoor(t *testing.T) {
	const door = "; run: nova-check help"

	exit, _, stderr := runCheck(t, "version", "extra")
	if exit != 2 {
		t.Fatalf("version extra: exit %d, want 2; stderr: %q", exit, stderr)
	}

	line := strings.TrimRight(stderr, "\n")
	if !strings.Contains(line, "takes no flags and no arguments") {
		t.Errorf("refusal no longer says what the input wants: %q", line)
	}
	if !strings.HasSuffix(line, door) {
		t.Errorf("refusal line does not END in the door: %q", line)
	}
	if got := strings.Count(line, door); got != 1 {
		t.Errorf("the door appears %d times, want exactly 1: %q", got, line)
	}
}
