package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A probe that plants both at once, on the certification schedule, and asserts the refusal
// lines is the canary (issue #233). The #226 tests cover each site's guarded read; the two
// dispatcher reads of a job's RESULT.md -- the batch gather (batch.go) and the single-card
// check (contract.go) -- read with os.ReadFile and walked straight past the wall. These
// plant a symlink and a FIFO at RESULT.md and ask that each be refused with one line naming
// the path and the kind, never followed and never blocked on.

// The single-card contract read refuses a symlink at RESULT.md and never follows it.
func TestCheckResultRefusesAPlantedSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	secret := filepath.Join(dir, "secret-outside-the-wall")
	if err := os.WriteFile(secret, []byte("a secret the wall was keeping\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	job := filepath.Join(dir, "job")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	plantLink(t, secret, ResultPath(job))
	_, err := CheckResult(ResultPath(job), Contract{Label: "a", ContractLine: "RESULT: a"})
	if err == nil {
		t.Fatal("CheckResult read through a planted symlink and raised nothing")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("the refusal does not name the kind symlink: %v", err)
	}
}

// The sequence both probes walk together: a symlink at RESULT.md first, then a FIFO, each
// refused with its kind named and never followed or blocked on.
func TestFriendSequencePlantedResultIsRefused(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	secret := filepath.Join(dir, "secret-outside-the-wall")
	if err := os.WriteFile(secret, []byte("a secret the wall was keeping\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	symJob := filepath.Join(dir, "job-symlink")
	if err := os.MkdirAll(symJob, 0o755); err != nil {
		t.Fatal(err)
	}
	plantLink(t, secret, ResultPath(symJob))
	if _, err := readFileSteady(ResultPath(symJob)); err == nil {
		t.Fatal("the steady read read through a planted symlink")
	} else if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("the symlink refusal does not name the kind: %v", err)
	}

	fifoJob := filepath.Join(dir, "job-fifo")
	if err := os.MkdirAll(fifoJob, 0o755); err != nil {
		t.Fatal(err)
	}
	plantFIFO(t, ResultPath(fifoJob))
	done := make(chan error, 1)
	go func() {
		_, err := readFileSteady(ResultPath(fifoJob))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a FIFO read as a published report")
		}
		if !strings.Contains(err.Error(), "fifo") {
			t.Fatalf("the fifo refusal does not name the kind: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("STILL BLOCKED after 30s: the dispatcher is wedged")
	}
}
