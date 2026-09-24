package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nova-swarm pull --submit takes one card from queue/lanes/ into taken/ and writes its Job
// manifest, and declines while cores*1.5 - load1 <= 0 (docs/SPEC-FLEET-KUBE.md Part 2).
func TestPullSubmitVerbTakesOneCardAndWritesItsJob(t *testing.T) {
	bench := t.TempDir()
	jobs := filepath.Join(t.TempDir(), "manifests")
	lane := filepath.Join(bench, "queue", "lanes", "next")
	if err := os.MkdirAll(lane, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.card", "b.card"} {
		if err := os.WriteFile(filepath.Join(lane, name), []byte("card\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	args := func(load1 string) []string {
		return []string{"pull", "--submit", "--bench", bench, "--worker", "w1", "--cores", "2",
			"--load1", load1, "--image", "nova-card:test", "--runner", "nova-card run", "--jobs", jobs}
	}

	exit, stdout, stderr := runSwarm(t, args("3.0")...)
	if exit != 0 || !strings.Contains(stdout, "declined=true card=-") {
		t.Fatalf("at the load line: exit=%d stdout=%q stderr=%q, want declined=true card=-", exit, stdout, stderr)
	}
	if entries, _ := os.ReadDir(jobs); len(entries) != 0 {
		t.Fatalf("a decline wrote %d manifests", len(entries))
	}

	exit, stdout, stderr = runSwarm(t, args("2.5")...)
	if exit != 0 || !strings.Contains(stdout, "declined=false card=a") {
		t.Fatalf("under the load line: exit=%d stdout=%q stderr=%q, want declined=false card=a", exit, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(bench, "taken", "w1-a.card")); err != nil {
		t.Fatalf("taken/w1-a.card missing: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(jobs, "card-w1-a.yaml"))
	if err != nil {
		t.Fatalf("Job manifest missing: %v", err)
	}
	for _, want := range []string{`"kind": "Job"`, `"ephemeral-storage": "2Gi"`, `"limits"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("manifest lacks %s:\n%s", want, raw)
		}
	}
	if _, err := os.Stat(filepath.Join(lane, "b.card")); err != nil {
		t.Fatalf("one turn took more than one card: %v", err)
	}
}
