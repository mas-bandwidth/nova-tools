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
	workerDir := filepath.Join(dir, "worker-1")
	if err := os.MkdirAll(workerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nonce := "prepare-nonce"
	if err := writeSlot(p.slotPath(1), SlotFile{Job: id, JobDir: jobDir, State: SlotReserved, Nonce: nonce}); err != nil {
		t.Fatal(err)
	}
	// A directory at the pending task destination makes the rollback rename fail.
	if err := os.MkdirAll(p.taskFile(Pending, id), 0o755); err != nil {
		t.Fatal(err)
	}
	run := RunInput{Pool: p, Worker: Worker{WorkerDir: workerDir}, Workers: 0, NoSandbox: true, Now: func() time.Time { return time.Unix(10, 0).UTC() }}
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
	// The real next dispatcher must preserve the reservation while the task move
	// remains blocked, rather than silently freeing an ownerless running task.
	var first strings.Builder
	if got := Run(RunInput{Pool: p, Worker: run.Worker, Workers: 0, NoSandbox: true, Stdout: &first, Stderr: &first, Now: run.Now}); got != 1 || !strings.Contains(first.String(), "unlaunched recovery failed") {
		t.Fatalf("blocked recovery must quarantine and retain the slot: code=%d output=%q", got, first.String())
	}
	if _, err := p.ReadSlot(1); err != nil {
		t.Fatalf("blocked recovery released the reservation: %v", err)
	}
	if err := os.Remove(p.taskFile(Pending, id)); err != nil {
		t.Fatal(err)
	}
	var second strings.Builder
	if got := Run(RunInput{Pool: p, Worker: run.Worker, Workers: 0, NoSandbox: true, Stdout: &second, Stderr: &second, Now: run.Now}); got != 1 || !strings.Contains(second.String(), "RUN RECLAIM slot=1") {
		t.Fatalf("repaired recovery must reclaim the slot: code=%d output=%q", got, second.String())
	}
	if _, err := p.ReadSlot(1); !os.IsNotExist(err) {
		t.Fatalf("repaired recovery did not release the reservation: %v", err)
	}
}

func TestUnlaunchedRecoveryHandlesAlreadyPendingTask(t *testing.T) {
	dir := t.TempDir()
	p, err := OpenPool(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := "pending-after-claim"
	if err := p.Add([]byte("task"), Sidecar{ID: id, Files: 1, RC: -1}); err != nil {
		t.Fatal(err)
	}
	jobDir := filepath.Join(dir, "worker-1", "jobs", id)
	workerDir := filepath.Join(dir, "worker-1")
	if err := os.MkdirAll(workerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nonce := "pending-nonce"
	if err := WriteJSON(AbortedPath(jobDir), AbortedRecord{Nonce: nonce, Reason: "preparation failed", At: Stamp(time.Unix(10, 0).UTC())}); err != nil {
		t.Fatal(err)
	}
	if err := writeSlot(p.slotPath(1), SlotFile{Job: id, JobDir: jobDir, State: SlotReserved, Nonce: nonce}); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if got := Run(RunInput{Pool: p, Worker: Worker{WorkerDir: workerDir}, Workers: 0, NoSandbox: true, Stdout: &out, Stderr: &out, Now: func() time.Time { return time.Unix(10, 0).UTC() }}); got != 1 || !strings.Contains(out.String(), "RUN RECLAIM slot=1") {
		t.Fatalf("already-pending recovery failed: code=%d output=%q", got, out.String())
	}
	if _, err := p.ReadSlot(1); !os.IsNotExist(err) {
		t.Fatalf("already-pending recovery retained slot: %v", err)
	}
	sc, err := p.ReadSidecar(Pending, id)
	if err != nil || sc.Launch != "unlaunched" {
		t.Fatalf("pending sidecar was not repaired: sc=%+v err=%v", sc, err)
	}
}
