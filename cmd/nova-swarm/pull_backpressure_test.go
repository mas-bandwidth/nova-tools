package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The help names the pull verb from SPEC-JOBS.md section 7.
func TestHelpNamesThePullVerb(t *testing.T) {
	exit, stdout, stderr := runSwarm(t, "help")
	if exit != 0 {
		t.Fatalf("help exit = %d, stderr=%s", exit, stderr)
	}
	if !strings.Contains(stdout, "nova-swarm pull") {
		t.Fatalf("help does not name the pull verb:\n%s", stdout)
	}
}

// A full bench pulls nothing and leaves its cards in queue/; a bench under its line takes
// up to the line. The pull reads the probe numbers from flags, so this is offline.
func TestPullLeavesCardsWhenTheBenchIsFull(t *testing.T) {
	bench := t.TempDir()
	queue := filepath.Join(bench, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.card", "b.card", "c.card"} {
		if err := os.WriteFile(filepath.Join(queue, name), []byte("card\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// cores 1, no load, plenty of disk and memory: line = 1.
	full := []string{"pull", "--bench", bench, "--worker", "w1",
		"--cores", "1", "--load1", "0", "--free-gb", "100", "--memfree-gb", "100", "--running", "1"}
	exit, stdout, _ := runSwarm(t, full...)
	if exit != 0 {
		t.Fatalf("full pull exit = %d", exit)
	}
	if !strings.Contains(stdout, "taken=0 left=3") {
		t.Fatalf("full bench pull = %q, want taken=0 left=3", stdout)
	}
	if got := countPullCards(t, queue); got != 3 {
		t.Fatalf("full bench left %d cards, want 3", got)
	}

	under := []string{"pull", "--bench", bench, "--worker", "w1",
		"--cores", "4", "--load1", "0", "--free-gb", "100", "--memfree-gb", "100", "--running", "0"}
	exit, stdout, _ = runSwarm(t, under...)
	if exit != 0 {
		t.Fatalf("under pull exit = %d", exit)
	}
	if !strings.Contains(stdout, "taken=3 left=0") {
		t.Fatalf("under-line pull = %q, want taken=3 left=0", stdout)
	}
}

func TestPullRefusesAMissingBench(t *testing.T) {
	exit, _, stderr := runSwarm(t, "pull", "--worker", "w1", "--cores", "1", "--load1", "0",
		"--free-gb", "100", "--memfree-gb", "100")
	if exit != 2 {
		t.Fatalf("pull without --bench exit = %d, want 2", exit)
	}
	if !strings.Contains(stderr, "--bench is required") {
		t.Fatalf("stderr = %q, want the --bench remedy", stderr)
	}
}

func countPullCards(t *testing.T, dir string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.card"))
	if err != nil {
		t.Fatal(err)
	}
	return len(matches)
}
