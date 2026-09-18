package swarm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// an-expired-lease-with-a-dead-pid-returns-the-card (docs/SPEC-JOBS.md section 3):
// a worker that dies holding a card leaves its lease past until= with a dead pid;
// the next take reaps that lease and its card goes back to queue/.
func TestAnExpiredLeaseWithADeadPidReturnsTheCard(t *testing.T) {
	const deadPid = 2147483647
	if Alive(deadPid, "") {
		t.Skip("dead pid probe is alive here")
	}
	store := writeSlotStore(t, "capacity\t2\nreserve\t0\nalice\t2\n")
	// The dead worker had taken a.card: it sits in taken/ under its owner, and its
	// lease is past until= with a pid that is gone.
	if err := os.MkdirAll(filepath.Join(store, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "taken", "alice-a.card"), []byte("card"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Hour)
	if err := MakeSlotLease(store, "dead-1", "alice", deadPid, "a.card", past); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	returned, err := ReapExpiredLeases(store, now)
	if err != nil {
		t.Fatalf("ReapExpiredLeases: %v", err)
	}
	if len(returned) != 1 || returned[0] != "a.card" {
		t.Fatalf("reap returned %v, want [a.card]", returned)
	}
	if _, err := os.Stat(filepath.Join(store, "queue", "a.card")); err != nil {
		t.Fatalf("card must be back in queue/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store, "taken", "alice-a.card")); !os.IsNotExist(err) {
		t.Fatalf("taken card must be gone, stat err = %v", err)
	}
	leases, err := ListSlotLeases(store, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 0 {
		t.Fatalf("expired dead lease must be gone, got %+v", leases)
	}
}

// A pull takes the first queued card under a fresh lease and moves it to taken/;
// a heartbeat then renews that lease. This pins the happy path the two named red
// tests only guard the edges of.
func TestPullTakesACardUnderALeaseAndHeartbeatRenewsIt(t *testing.T) {
	store := writeSlotStore(t, "capacity\t2\nreserve\t0\nalice\t2\n")
	if err := os.MkdirAll(filepath.Join(store, "queue"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "queue", "a.card"), []byte("card"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	res, err := PullCard(store, "alice", time.Minute, now, os.Getpid())
	if err != nil {
		t.Fatalf("PullCard: %v", err)
	}
	if res.Card != "a.card" || res.Lease == "" {
		t.Fatalf("pull = %+v, want a.card under a lease", res)
	}
	if _, err := os.Stat(filepath.Join(store, "taken", "alice-a.card")); err != nil {
		t.Fatalf("taken card missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store, "queue", "a.card")); !os.IsNotExist(err) {
		t.Fatalf("the queue must no longer hold the taken card")
	}
	renewed, until, err := RenewSlotLease(store, "alice", "a.card", time.Hour, now.Add(30*time.Second))
	if err != nil || renewed != 1 {
		t.Fatalf("heartbeat renewed=%d err=%v, want 1", renewed, err)
	}
	if !until.After(res.Until) {
		t.Fatalf("heartbeat until %s must be after the take's %s", until, res.Until)
	}
}

// A slots directory that is a symlink out of the store carries a lease whose computed
// path escapes the store root. safepath.RemoveUnder refuses it, so the directory the link
// points at -- and the lease inside it -- survives. A raw os.RemoveAll would follow the
// link and delete outside the store.
func TestReapRefusesALeaseThatEscapesTheStoreThroughASymlink(t *testing.T) {
	const deadPid = 2147483647
	if Alive(deadPid, "") {
		t.Skip("dead pid probe is alive here")
	}
	store := t.TempDir()
	outside := t.TempDir()
	survivor := filepath.Join(outside, "survivor")
	if err := os.WriteFile(survivor, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The slots directory is a symlink to a directory outside the store: the lease
	// directory the reaper computes sits under the link, outside the store root.
	if err := os.Symlink(outside, filepath.Join(store, "slots")); err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Hour)
	if err := MakeSlotLease(store, "dead-1", "alice", deadPid, "a.card", past); err != nil {
		t.Fatal(err)
	}

	if _, err := ReapExpiredLeases(store, time.Now().UTC()); err != nil {
		t.Fatalf("ReapExpiredLeases: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "dead-1")); err != nil {
		t.Fatalf("the reaper followed the slots symlink and removed the lease outside the store: %v", err)
	}
	if _, err := os.Stat(survivor); err != nil {
		t.Fatalf("the directory the slots symlink pointed at was removed: %v", err)
	}
}
