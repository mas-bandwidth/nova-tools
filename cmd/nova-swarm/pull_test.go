package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// a-launch-without-a-lease-is-refused-by-the-puller (docs/SPEC-JOBS.md section 3):
// `nova-swarm pull` takes a slots take lease from the bench store before it runs a
// card; when the share is spent the take refuses and the puller refuses with it,
// leaving the card in queue/.
func TestALaunchWithoutALeaseIsRefusedByThePuller(t *testing.T) {
	store := t.TempDir()
	if err := os.WriteFile(filepath.Join(store, "shares.tsv"), []byte("capacity\t1\nreserve\t0\nalice\t0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(store, "queue"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "queue", "a.card"), []byte("card"), 0o644); err != nil {
		t.Fatal(err)
	}

	exit, stdout, stderr := runSwarm(t, "pull", "--store", store, "--owner", "alice", "--for", "1h")
	if exit != 2 {
		t.Fatalf("pull without a lease exit = %d, want 2 (stdout=%q stderr=%q)", exit, stdout, stderr)
	}
	if !strings.Contains(stderr, "pull") || !strings.Contains(stderr, "lease") {
		t.Fatalf("refusal must name the puller and the lease, got %q", stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("a refused pull wrote to stdout: %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(store, "queue", "a.card")); err != nil {
		t.Fatalf("refused pull must leave the card in queue/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store, "taken")); !os.IsNotExist(err) {
		t.Fatalf("refused pull must take nothing, taken/ stat err = %v", err)
	}
}
