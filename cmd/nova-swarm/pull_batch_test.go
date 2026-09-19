package main

import (
	"strings"
	"testing"
)

// docs/SPEC-JOBS.md section 6 names the pull verb and the flags one batch carries.
func TestPullVerbLineIsInTheHelp(t *testing.T) {
	_, stdout, _ := runSwarm(t, "help")
	for _, want := range []string{"nova-swarm pull", "--batch <n>", "--clone <dir>", "--harvest <dir>", "--queue <dir>"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("help does not name %q:\n%s", want, stdout)
		}
	}
}

// A pull missing one required flag is a refusal: exit 2, one remedy line, nothing on stdout.
func TestPullWithoutAQueueRefusesOneLine(t *testing.T) {
	dir := t.TempDir()
	exit, stdout, stderr := runSwarm(t, "pull",
		"--clone", dir, "--harvest", dir, "--batch", "1", "--runner", "true")
	if exit != 2 {
		t.Fatalf("pull with no queue exit = %d, want 2 (stdout=%q stderr=%q)", exit, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("a refusal wrote to stdout: %q", stdout)
	}
	if lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n"); len(lines) != 1 {
		t.Fatalf("a refusal printed %d lines, want 1:\n%s", len(lines), stderr)
	}
	if !strings.Contains(stderr, "--queue is required") || !strings.Contains(stderr, "it wants") {
		t.Fatalf("the refusal names neither the flag nor what it wants:\n%s", stderr)
	}
}
