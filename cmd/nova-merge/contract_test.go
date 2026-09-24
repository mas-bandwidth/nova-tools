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

// Demanded test 20: init is the ONE creation verb, and every other verb refuses a
// directory that is not a lane -- with the init command in the refusal, and nothing
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
			contains(t, stderr, "nova-merge init --lane")
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
