package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeSlotStore(t *testing.T, shares string) string {
	t.Helper()
	store := t.TempDir()
	if err := os.WriteFile(filepath.Join(store, "shares.tsv"), []byte(shares), 0o644); err != nil {
		t.Fatal(err)
	}
	return store
}

func mustSlotTake(t *testing.T, store, owner string, k int, d time.Duration, label string, now time.Time, pid int) (bool, int, int, int, int, string) {
	t.Helper()
	ids, held, share, free, holders, ok, err := TakeSlotLeases(store, owner, k, d, label, now, pid)
	if err != nil {
		t.Fatalf("TakeSlotLeases: %v", err)
	}
	return ok, len(ids), held, share, free, holders
}

// Two owners at share 2 each with capacity 4 reserve 0 cannot take a fifth.
func TestSlotSharesRefuseFifth(t *testing.T) {
	store := writeSlotStore(t, "capacity\t4\nreserve\t0\nalice\t2\nbob\t2\n")
	now := time.Now().UTC()
	pid := os.Getpid()
	if ok, _, _, _, _, _ := mustSlotTake(t, store, "alice", 2, time.Hour, "", now, pid); !ok {
		t.Fatalf("alice take 2 should grant")
	}
	if ok, _, _, _, _, _ := mustSlotTake(t, store, "bob", 2, time.Hour, "", now, pid); !ok {
		t.Fatalf("bob take 2 should grant")
	}
	ok, _, held, share, free, holders := mustSlotTake(t, store, "alice", 1, time.Hour, "", now, pid)
	if ok {
		t.Fatalf("fifth lease must refuse: held=%d share=%d free=%d holders=%s", held, share, free, holders)
	}
	if held != 2 || share != 2 || free != 0 {
		t.Fatalf("refused counts wrong: held=%d share=%d free=%d holders=%s", held, share, free, holders)
	}
	if holders != "alice:2,bob:2" {
		t.Fatalf("holders must name both owners, got %q", holders)
	}
}

// An expired lease with a dead pid is reaped and its slot granted.
func TestSlotExpiredDeadPidReaped(t *testing.T) {
	const deadPid = 2147483647
	if Alive(deadPid, "") {
		t.Skip("dead pid probe is alive here")
	}
	store := writeSlotStore(t, "capacity\t2\nreserve\t0\nalice\t2\n")
	past := time.Now().UTC().Add(-time.Hour)
	if err := MakeSlotLease(store, "old-1", "alice", deadPid, "", past); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ok, granted, _, _, _, _ := mustSlotTake(t, store, "alice", 1, time.Hour, "", now, os.Getpid())
	if !ok || granted != 1 {
		t.Fatalf("expired dead lease must be reaped and granted: ok=%v granted=%d", ok, granted)
	}
	if _, err := os.Stat(filepath.Join(store, "slots", "old-1")); !os.IsNotExist(err) {
		t.Fatalf("reaped lease dir must be gone")
	}
	leases, err := ListSlotLeases(store, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 || leases[0].Owner != "alice" {
		t.Fatalf("one fresh lease must remain: %+v", leases)
	}
}

// An expired lease with the test's live pid stays and is printed DRIFT.
func TestSlotExpiredLivePidDrift(t *testing.T) {
	store := writeSlotStore(t, "capacity\t2\nreserve\t0\nalice\t1\n")
	past := time.Now().UTC().Add(-time.Hour)
	if err := MakeSlotLease(store, "drift-1", "alice", os.Getpid(), "", past); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	leases, err := ListSlotLeases(store, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 {
		t.Fatalf("drift lease must stay: %+v", leases)
	}
	if got := leases[0].State(now); got != "DRIFT" {
		t.Fatalf("expired live lease state must be DRIFT, got %q", got)
	}
	ok, _, held, _, _, _ := mustSlotTake(t, store, "alice", 1, time.Hour, "", now, os.Getpid())
	if ok {
		t.Fatalf("DRIFT lease counts as held and must refuse past share")
	}
	if held != 1 {
		t.Fatalf("held must count DRIFT, got %d", held)
	}
	var line string
	for _, l := range leases {
		line = l.Line(now)
	}
	if !strings.Contains(line, "state=DRIFT") {
		t.Fatalf("list line must print DRIFT, got %q", line)
	}
}

// Take past capacity-reserve is refused naming holders.
func TestSlotCapacityReserveRefused(t *testing.T) {
	store := writeSlotStore(t, "capacity\t4\nreserve\t1\nalice\t10\nbob\t10\n")
	now := time.Now().UTC()
	pid := os.Getpid()
	if ok, _, _, _, _, _ := mustSlotTake(t, store, "alice", 2, time.Hour, "", now, pid); !ok {
		t.Fatalf("alice take 2 should grant")
	}
	if ok, _, _, _, _, _ := mustSlotTake(t, store, "bob", 1, time.Hour, "", now, pid); !ok {
		t.Fatalf("bob take 1 should grant (total 3 = capacity-reserve)")
	}
	ok, _, _, _, free, holders := mustSlotTake(t, store, "bob", 1, time.Hour, "", now, pid)
	if ok {
		t.Fatalf("take past capacity-reserve must refuse")
	}
	if free != 0 {
		t.Fatalf("free must be 0 at capacity-reserve, got %d", free)
	}
	if holders != "alice:2,bob:1" {
		t.Fatalf("refusal must name holders, got %q", holders)
	}
}

// aBenchSlotStore is a store with room, for a test whose batch launches `nova-swarm
// native`. Since nova-tools#1546 a launch without a lease is REFUSED, so a Batch with no
// --runner of its own needs SlotsStore and SlotOwner or it never reaches the card.
func aBenchSlotStore(t *testing.T) string {
	t.Helper()
	store := filepath.Join(t.TempDir(), "slots-store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "shares.tsv"), []byte("capacity\t8\nreserve\t0\nfake-1\t8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return store
}
