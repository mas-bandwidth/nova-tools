package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// plantPullCard puts one <name>.card in a bench's queue/.
func plantPullCard(t *testing.T, bench, name string) {
	t.Helper()
	queue := filepath.Join(bench, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatalf("mkdir queue: %v", err)
	}
	if err := os.WriteFile(filepath.Join(queue, name+".card"), []byte("card\n"), 0o644); err != nil {
		t.Fatalf("write card: %v", err)
	}
}

func invokePull(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(""), &stdout, &stderr, time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC))
	return code, stdout.String(), stderr.String()
}

// The help text carries the pull verb line and the rename that is the ownership
// record, exactly as section 2 prints them.
func TestHelpNamesThePullVerbAndItsRename(t *testing.T) {
	code, stdout, _ := invokePull("help")
	if code != 0 {
		t.Fatalf("help exit = %d, want 0", code)
	}
	for _, want := range []string{"nova-swarm pull", "rename(<name>.card, taken/<worker>-<name>.card)"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("help does not name %q:\n%s", want, stdout)
		}
	}
}

// pull lists a bench's queue/ and takes one card by rename, as one line.
func TestPullTakesOneCardByRename(t *testing.T) {
	bench := t.TempDir()
	plantPullCard(t, bench, "alpha")

	code, stdout, stderr := invokePull("pull", "--bench", bench, "--worker", "w1")
	if code != 0 {
		t.Fatalf("pull exit = %d, want 0 (stdout=%q stderr=%q)", code, stdout, stderr)
	}
	line := strings.TrimSpace(stdout)
	if strings.Count(stdout, "\n") != 1 {
		t.Fatalf("pull printed more than one line: %q", stdout)
	}
	for _, want := range []string{"PULL", "bench=" + bench, "worker=w1", "card=alpha", "source=queue"} {
		if !strings.Contains(line, want) {
			t.Fatalf("pull line %q does not name %q", line, want)
		}
	}
	if _, err := os.Stat(filepath.Join(bench, "queue", "alpha.card")); !os.IsNotExist(err) {
		t.Fatalf("the card is still in queue/: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(bench, "taken", "w1-alpha.card")); err != nil {
		t.Fatalf("the ownership record w1-alpha.card is missing: %v", err)
	}
}

// An empty queue with nowhere to steal is a normal idle line, not a refusal.
func TestPullOnAnEmptyQueuePrintsIdle(t *testing.T) {
	bench := t.TempDir()
	code, stdout, stderr := invokePull("pull", "--bench", bench, "--worker", "w1")
	if code != 0 {
		t.Fatalf("pull on an empty queue exit = %d, want 0 (stderr=%q)", code, stderr)
	}
	line := strings.TrimSpace(stdout)
	for _, want := range []string{"card=-", "source=none"} {
		if !strings.Contains(line, want) {
			t.Fatalf("idle line %q does not name %q", line, want)
		}
	}
}

// A missing --bench or --worker is a refusal with the remedy, exit 2.
func TestPullRefusesMissingFlags(t *testing.T) {
	code, stdout, stderr := invokePull("pull")
	if code != 2 {
		t.Fatalf("pull with no flags exit = %d, want 2 (stdout=%q stderr=%q)", code, stdout, stderr)
	}
	for _, want := range []string{"--bench is required", "--worker is required", "nova-swarm help"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("refusal %q does not name %q", stderr, want)
		}
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("a refusal wrote to stdout: %q", stdout)
	}
}

// An idle worker steals from the fullest bench, never below the victim's capacity
// line.
func TestPullStealsFromTheFullestBench(t *testing.T) {
	home := t.TempDir()
	victim := t.TempDir()
	for i := 0; i < 5; i++ {
		plantPullCard(t, victim, "v"+string(rune('a'+i)))
	}

	code, stdout, stderr := invokePull("pull", "--bench", home, "--worker", "w1",
		"--steal", victim, "--capacity", "2")
	if code != 0 {
		t.Fatalf("stealing pull exit = %d, want 0 (stdout=%q stderr=%q)", code, stdout, stderr)
	}
	line := strings.TrimSpace(stdout)
	for _, want := range []string{"source=steal", "victim=" + victim} {
		if !strings.Contains(line, want) {
			t.Fatalf("steal line %q does not name %q", line, want)
		}
	}
	left, err := os.ReadDir(filepath.Join(victim, "queue"))
	if err != nil {
		t.Fatalf("victim queue: %v", err)
	}
	if len(left) != 2 {
		t.Fatalf("victim left with %d cards, want its capacity line 2", len(left))
	}
}
