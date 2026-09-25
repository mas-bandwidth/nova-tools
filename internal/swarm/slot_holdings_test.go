package swarm

import (
	"os"
	"testing"
	"time"
)

// b1c69694 added SlotHoldings as the read status prints. Reverting slots.go
// kept this package green: take/list tests still passed, and nothing called
// SlotHoldings.
func TestSlotHoldingsCountsTheOwnerAndNamesTheShare(t *testing.T) {
	t.Parallel()

	store := writeSlotStore(t, "capacity\t4\nreserve\t0\nalice\t2\nbob\t2\n")
	now := time.Now().UTC()
	pid := os.Getpid()
	if ok, _, _, _, _, _ := mustSlotTake(t, store, "alice", 2, time.Hour, "a", now, pid); !ok {
		t.Fatal("alice take 2 should grant")
	}
	if ok, _, _, _, _, _ := mustSlotTake(t, store, "bob", 1, time.Hour, "b", now, pid); !ok {
		t.Fatal("bob take 1 should grant")
	}
	held, share, err := SlotHoldings(store, "alice", now)
	if err != nil {
		t.Fatal(err)
	}
	if held != 2 || share != 2 {
		t.Fatalf("alice held=%d share=%d, want 2 and 2", held, share)
	}
	held, share, err = SlotHoldings(store, "bob", now)
	if err != nil {
		t.Fatal(err)
	}
	if held != 1 || share != 2 {
		t.Fatalf("bob held=%d share=%d, want 1 and 2", held, share)
	}
	held, share, err = SlotHoldings(store, "carol", now)
	if err != nil {
		t.Fatal(err)
	}
	if held != 0 || share != 0 {
		t.Fatalf("an owner with no share row held=%d share=%d, want 0 and 0", held, share)
	}
}
