package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// Work list 10: the contract tests. Every exit code, every refusal sentence, the
// structural refusals, the capped listing, and the properties a source test is the only
// way to assert.

// Demanded test 20: every verb refuses a directory that is not a lane -- with the way
// to make one in the refusal (by hand: init is retired, FG-A fix 2), and nothing
// written on the way past.
func TestEveryOtherVerbRefusesADirectoryThatIsNotALane(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	empty := filepath.Join(l.dir, "not-a-lane")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "--lane", empty, "--pr", "1"},
		{"add-branch", "--lane", empty, "--branch", "b"},
		{"read", "--lane", empty, "--pr", "1", "--who", "emma", "--head", strings.Repeat("a", 40), "--verdict", "approve"},
		{"gate", "--lane", empty, "--pr", "1", "--head", strings.Repeat("a", 40), "--base-sha", strings.Repeat("b", 40),
			"--merge", strings.Repeat("c", 40), "--verdict", "green", "--summary", l.summary("not-a-lane")},
	} {
		t.Run(args[0], func(t *testing.T) {
			exit, stdout, stderr := l.run(args...)
			if exit != 2 {
				t.Fatalf("exit %d, want 2\n%s\n%s", exit, stdout, stderr)
			}
			contains(t, stderr, "refusing to guess: this is not a lane")
			contains(t, stderr, "nova-merge init is retired")
			contains(t, stderr, merge.LaneTemplate)
			if entries, _ := os.ReadDir(empty); len(entries) != 0 {
				t.Errorf("a refusal writes nothing on the way past; the directory holds %v", entries)
			}
		})
	}
}

func TestASecondInitIsRefusedAndTheStateIsByteIdentical(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	before, err := os.ReadFile(merge.StatePath(l.lane))
	if err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr := l.run("init", "--lane", l.lane, "--repo", "o/n", "--base", "other", "--lane-branch", "nova-merge/lane")
	if exit != 1 {
		t.Fatalf("an init of a lane that exists is exit 1 (the verb ran and said NO), got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "INIT REFUSED")
	after, _ := os.ReadFile(merge.StatePath(l.lane))
	if string(before) != string(after) {
		t.Error("a refused init leaves the state byte-identical")
	}
}

// TestNotALaneRemedyRunThroughTheBinary (FG-A fix 2): the refusal's manual step,
// followed verbatim -- write the template it prints as <lane>/state.json -- makes a lane
// the same verb then reads. The verb it names to run next exists (the audit probe
// TestRowanAuditLaneRemedyVerbExists runs it with -h).
func TestNotALaneRemedyRunThroughTheBinary(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	lane := filepath.Join(l.dir, "by-hand")
	if err := os.MkdirAll(lane, 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"read", "--lane", lane, "--pr", "1", "--who", "emma", "--head", strings.Repeat("a", 40), "--verdict", "approve"}
	exit, _, stderr := l.run(args...)
	if exit != 2 || !strings.Contains(stderr, "this is not a lane") {
		t.Fatalf("exit %d\n%s", exit, stderr)
	}
	// The step, as printed: "... and write <path> as <json>".
	_, step, ok := strings.Cut(stderr, " and write "+merge.StatePath(lane)+" as ")
	if !ok {
		t.Fatalf("the refusal carries no write step: %q", stderr)
	}
	body := strings.TrimSpace(step)
	if err := os.WriteFile(merge.StatePath(lane), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, _, stderr = l.run(args...)
	if strings.Contains(stderr, "this is not a lane") {
		t.Fatalf("after the manual step the directory is still not a lane: exit %d\n%s", exit, stderr)
	}
}
