package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPullWorkerRunsCardWithRunnerAndHarvestsOutcome: test the pull worker daemon CLI
// with --bench, --slots, --seat, --runner, --once.
func TestPullWorkerRunsCardWithRunnerAndHarvestsOutcome(t *testing.T) {
	bench := t.TempDir()
	if err := os.WriteFile(filepath.Join(bench, "shares.tsv"), []byte("capacity\t2\nreserve\t0\nhulk\t2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plantPullCard(t, bench, "CARD-777")
	harvest := t.TempDir()

	runnerScript := "echo 'RESULT: CARD-777 pass' > RESULT.md"
	exit, stdout, stderr := invokePull("pull",
		"--bench", bench,
		"--slots", "1",
		"--seat", "hulk",
		"--harvest", harvest,
		"--runner", runnerScript,
		"--once")

	if exit != 0 {
		t.Fatalf("pull worker exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}

	if !strings.Contains(stdout, "PULL DONE") || !strings.Contains(stdout, "card=CARD-777.card") {
		t.Fatalf("stdout missing PULL DONE line:\n%s", stdout)
	}

	resFile := filepath.Join(harvest, "CARD-777", "RESULT.md")
	data, err := os.ReadFile(resFile)
	if err != nil {
		t.Fatalf("harvested RESULT.md not found at %s: %v", resFile, err)
	}
	if !strings.Contains(string(data), "RESULT: CARD-777 pass") {
		t.Fatalf("harvested content mismatch: %s", string(data))
	}
}

// TestPullWorkerIdleExitsZero: test pull worker with empty queue and --once exits 0 with PULL IDLE.
func TestPullWorkerIdleExitsZero(t *testing.T) {
	bench := t.TempDir()
	if err := os.WriteFile(filepath.Join(bench, "shares.tsv"), []byte("capacity\t2\nreserve\t0\nhulk\t2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	exit, stdout, stderr := invokePull("pull",
		"--bench", bench,
		"--slots", "1",
		"--seat", "hulk",
		"--once")

	if exit != 0 {
		t.Fatalf("pull worker idle exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "PULL IDLE") {
		t.Fatalf("stdout missing PULL IDLE:\n%s", stdout)
	}
}

// TestPullLeaseTakesACardUnderALease: --store/--owner/--for takes a card under a lease.
func TestPullLeaseTakesACardUnderALease(t *testing.T) {
	store := t.TempDir()
	if err := os.WriteFile(filepath.Join(store, "shares.tsv"), []byte("capacity\t2\nreserve\t0\nalice\t2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(store, "queue"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "queue", "a.card"), []byte("card"), 0o644); err != nil {
		t.Fatal(err)
	}

	exit, stdout, stderr := invokePull("pull", "--store", store, "--owner", "alice", "--for", "1h")
	if exit != 0 {
		t.Fatalf("pull lease exit = %d, want 0 (stdout=%q stderr=%q)", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "PULL OK") || !strings.Contains(stdout, "card=a.card") {
		t.Fatalf("pull lease output missing card:\n%s", stdout)
	}
}
