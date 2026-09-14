package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreparationRollbackRetainsNonceReservationWhenTaskMoveFails(t *testing.T) {
	dir := t.TempDir()
	p, err := OpenPool(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := "prepare-rollback"
	if err := p.Add([]byte("task"), Sidecar{ID: id, Files: 1, RC: -1}); err != nil {
		t.Fatal(err)
	}
	if err := p.Claim(id, Pending, Running); err != nil {
		t.Fatal(err)
	}
	jobDir := filepath.Join(dir, "worker-1", "jobs", id)
	nonce := "prepare-nonce"
	if err := writeSlot(p.slotPath(1), SlotFile{Job: id, JobDir: jobDir, State: SlotReserved, Nonce: nonce}); err != nil {
		t.Fatal(err)
	}
	// A directory at the pending task destination makes the rollback rename fail.
	if err := os.MkdirAll(p.taskFile(Pending, id), 0o755); err != nil {
		t.Fatal(err)
	}
	run := RunInput{Pool: p, Now: func() time.Time { return time.Unix(10, 0).UTC() }}
	line, code := run.retainFailedPreparation(Sidecar{ID: id}, 1, jobDir, nonce, os.ErrNotExist, os.ErrPermission)
	if code != 1 || !strings.Contains(line, "reservation") {
		t.Fatalf("rollback failure must retain the reservation: code=%d line=%q", code, line)
	}
	if _, err := p.ReadSlot(1); err != nil {
		t.Fatalf("nonce-bound reservation was discarded: %v", err)
	}
	if _, err := os.Stat(p.taskFile(Running, id)); err != nil {
		t.Fatalf("running task disappeared while rollback was unresolved: %v", err)
	}
	var aborted AbortedRecord
	if err := ReadJSON(AbortedPath(jobDir), &aborted); err != nil {
		t.Fatalf("confirmed no-launch evidence was not retained: %v", err)
	}
	if aborted.Nonce != nonce || aborted.Survivors != 0 {
		t.Fatalf("aborted record is not bound to the reservation: %+v", aborted)
	}
}
