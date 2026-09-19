package swarm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ReleaseSlotLeasesByID is the release a HOLDER uses (nova-tools#1546, Stella's hold on
// PR #1562). Releasing by owner and label is not releasing by identity: an owner is a
// bench and a label is a card's name, and two runs sharing both would each give away the
// other's seat. These pin the three things that make it safe to hand a deferred cleanup.

func heldIDs(t *testing.T, store string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(store, "slots"))
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			out[e.Name()] = true
		}
	}
	return out
}

// It removes EXACTLY what it is given, with two leases that agree on owner AND label --
// the pair the old by-owner-and-label release could not tell apart.
func TestReleaseByIDTakesOnlyTheLeasesNamed(t *testing.T) {
	store := writeSlotStore(t, "capacity\t4\nreserve\t0\nbench\t4\n")
	now := time.Now().UTC()

	mine, _, _, _, _, ok, err := TakeSlotLeases(store, "bench", 1, time.Hour, "card-7", now, os.Getpid())
	if err != nil || !ok {
		t.Fatalf("first take: ok=%v err=%v", ok, err)
	}
	theirs, _, _, _, _, ok, err := TakeSlotLeases(store, "bench", 1, time.Hour, "card-7", now, os.Getpid())
	if err != nil || !ok {
		t.Fatalf("second take: ok=%v err=%v", ok, err)
	}
	if len(mine) != 1 || len(theirs) != 1 || mine[0] == theirs[0] {
		t.Fatalf("two takes must yield two distinct ids, got %v and %v", mine, theirs)
	}

	released, err := ReleaseSlotLeasesByID(store, mine, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if released != 1 {
		t.Errorf("released %d, want 1", released)
	}
	held := heldIDs(t, store)
	if held[mine[0]] {
		t.Errorf("the lease named was not removed")
	}
	if !held[theirs[0]] {
		t.Errorf("the OTHER lease, same owner and same label, was removed; that is the whole defect")
	}
}

// The pid is a fence: an id whose lease is no longer this holder's must be left alone, so
// a stale list cannot take a seat somebody else has since been granted.
func TestReleaseByIDLeavesALeaseThatIsNoLongerOurs(t *testing.T) {
	store := writeSlotStore(t, "capacity\t2\nreserve\t0\nbench\t2\n")
	ids, _, _, _, _, ok, err := TakeSlotLeases(store, "bench", 1, time.Hour, "card-7", time.Now().UTC(), os.Getpid())
	if err != nil || !ok {
		t.Fatalf("take: ok=%v err=%v", ok, err)
	}

	released, err := ReleaseSlotLeasesByID(store, ids, os.Getpid()+1)
	if err != nil {
		t.Fatal(err)
	}
	if released != 0 {
		t.Errorf("released %d, want 0: the lease carries another pid and is not ours to give away", released)
	}
	if !heldIDs(t, store)[ids[0]] {
		t.Error("the lease was removed although its pid is not the releaser's")
	}
}

// A release is allowed to be late. An id that is simply gone -- reaped, or released twice
// -- is not an error: the caller's job is to stop holding, not to prove nobody tidied up
// first.
func TestReleaseByIDIsQuietAboutALeaseThatIsAlreadyGone(t *testing.T) {
	store := writeSlotStore(t, "capacity\t2\nreserve\t0\nbench\t2\n")
	ids, _, _, _, _, ok, err := TakeSlotLeases(store, "bench", 1, time.Hour, "card-7", time.Now().UTC(), os.Getpid())
	if err != nil || !ok {
		t.Fatalf("take: ok=%v err=%v", ok, err)
	}
	if _, err := ReleaseSlotLeasesByID(store, ids, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	released, err := ReleaseSlotLeasesByID(store, append(ids, "never-existed", ""), os.Getpid())
	if err != nil {
		t.Fatalf("a second release is not an error: %v", err)
	}
	if released != 0 {
		t.Errorf("released %d, want 0", released)
	}
}

// The by-owner-and-label verb is UNCHANGED and this says so on purpose: `slots release`
// is a person's hand at a prompt, who wants exactly "free whatever this owner holds for
// that card" and who can see the store. A deferred cleanup cannot.
func TestReleaseByOwnerAndLabelStillTakesThemAll(t *testing.T) {
	store := writeSlotStore(t, "capacity\t4\nreserve\t0\nbench\t4\n")
	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		if _, _, _, _, _, ok, err := TakeSlotLeases(store, "bench", 1, time.Hour, "card-7", now, os.Getpid()); err != nil || !ok {
			t.Fatalf("take %d: ok=%v err=%v", i, ok, err)
		}
	}
	released, held, err := ReleaseSlotLeases(store, "bench", "card-7", false)
	if err != nil {
		t.Fatal(err)
	}
	if released != 2 || held != 0 {
		t.Errorf("released=%d held=%d, want 2 and 0: the by-hand verb frees every lease this owner holds for that label, which is what a person asking for it means", released, held)
	}
}
