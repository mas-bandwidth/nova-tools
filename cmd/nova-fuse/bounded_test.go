package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
)

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n")
}

// crowdedBox quarantines n surfaces through the tool's own verb, so the box under test is
// one this binary actually wrote rather than one a test invented.
func crowdedBox(t *testing.T, n int) string {
	t.Helper()
	box := filepath.Join(t.TempDir(), "fuse-box.json")
	for i := 0; i < n; i++ {
		exit, _, stderr := runFuse(t, "quarantine", "--box", box,
			fmt.Sprintf("a-surface-%03d", i), "a post addressed me and asked for a token")
		if exit != 0 {
			t.Fatalf("quarantine %d: exit %d; stderr: %s", i, exit, stderr)
		}
	}
	return box
}

// status is a verb to be GLANCED at, and at three hundred quarantined surfaces it was
// three hundred and one lines.
func TestStatusCountsAllAndListsAtMostMax(t *testing.T) {
	box := crowdedBox(t, 300)
	exit, stdout, stderr := runFuse(t, "status", "--box", box)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", exit, stderr)
	}
	// The count line, twenty listed, one MORE line.
	if got := countLines(stdout); got != bounded.Default+2 {
		t.Errorf("stdout is %d lines, want %d listed + count + MORE:\n%s", got, bounded.Default, stdout)
	}
	// THE COUNT IS NEVER CAPPED: this is the number the verb exists to report.
	if !strings.Contains(stdout, "STATUS OK lockdown=clear quarantines=300") {
		t.Errorf("the count line does not carry the whole total:\n%s", stdout)
	}
	if !strings.Contains(stdout, "STATUS MORE kind=quarantine shown=20 total=300") {
		t.Errorf("no MORE line naming the total:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--max") {
		t.Errorf("the MORE line names no remedy:\n%s", stdout)
	}
}

func TestStatusMaxWidensAndZeroListsAll(t *testing.T) {
	box := crowdedBox(t, 60)
	_, stdout, _ := runFuse(t, "status", "--box", box, "--max", "5")
	if got := countLines(stdout); got != 7 {
		t.Errorf("--max 5 gave %d lines, want count + 5 + MORE", got)
	}
	_, stdout, _ = runFuse(t, "status", "--box", box, "--max", "0")
	if got := countLines(stdout); got != 61 {
		t.Errorf("--max 0 gave %d lines, want count + all 60", got)
	}
	if strings.Contains(stdout, "STATUS MORE") {
		t.Errorf("--max 0 elided nothing and must print no MORE line")
	}
}

// A box with few enough surfaces to list is unchanged: no MORE line, and the same lines
// as before this cap existed.
func TestStatusUnderTheCeilingIsUnchanged(t *testing.T) {
	box := crowdedBox(t, 3)
	_, stdout, _ := runFuse(t, "status", "--box", box)
	if got := countLines(stdout); got != 4 {
		t.Errorf("stdout is %d lines, want count + 3:\n%s", got, stdout)
	}
	if strings.Contains(stdout, "STATUS MORE") {
		t.Errorf("a three-surface box printed a MORE line:\n%s", stdout)
	}
}

func TestStatusRefusesANegativeCeiling(t *testing.T) {
	box := crowdedBox(t, 1)
	exit, _, stderr := runFuse(t, "status", "--box", box, "--max", "-1")
	if exit != 2 || !strings.Contains(stderr, "--max must be a line ceiling") {
		t.Errorf("exit = %d, stderr = %q", exit, stderr)
	}
}

// A flag typo used to cost the whole 32-line banner, and a surface name beginning with a
// dash is the realistic shape here.
func TestARefusalIsOneLineAndNamesTheDoor(t *testing.T) {
	box := crowdedBox(t, 1)
	for _, args := range [][]string{
		{"status", "--boxx", box},
		{"defuse"},
		{"lockdown", "--box", box},
		{"quarantine", "--box", box},
		{"check", "--box", box, "a", "b"},
		{"path", "--box", box, "extra"},
		{"lift"},
		{"lift", "sideways"},
		nil,
	} {
		exit, stdout, stderr := runFuse(t, args...)
		if exit != 2 {
			t.Errorf("%v: exit = %d, want 2", args, exit)
		}
		if got := countLines(stderr); got != 1 {
			t.Errorf("%v: the refusal is %d lines, want 1:\n%s", args, got, stderr)
		}
		if !strings.Contains(stderr, "run: nova-fuse help") {
			t.Errorf("%v: the refusal names no door: %q", args, stderr)
		}
		if stdout != "" {
			t.Errorf("%v: a refusal wrote to stdout: %q", args, stdout)
		}
	}
	// `lift lockdown` is the one refusal here that is NOT one line, and it is meant to
	// be read rather than scanned. It must stay that way.
	exit, _, stderr := runFuse(t, "lift", "lockdown")
	if exit != 2 || !strings.Contains(stderr, "REFUSED, forever, by design") {
		t.Errorf("the hard refusal changed shape: exit %d, %q", exit, stderr)
	}
	exit, stdout, _ := runFuse(t, "help")
	if exit != 0 || !strings.Contains(stdout, "usage:") {
		t.Errorf("`help` did not print the usage: exit %d", exit)
	}
}
