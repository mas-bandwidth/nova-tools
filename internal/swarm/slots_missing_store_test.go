package swarm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A --store with no shares.tsv is not an empty store (Glenn's failure-guidance
// requirement, 2026-09-26): list and release refuse it, name the path and the
// `slots init` that makes one, and never make a lock file inside it.
func TestSlotStoreMissingIsRefusedNotEmpty(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "no-such-store")
	now := time.Now().UTC()

	_, err := ListSlotLeases(missing, now)
	var me *SlotStoreMissingError
	if !errors.As(err, &me) || me.Store != missing {
		t.Fatalf("list on a missing store: err %v, want *SlotStoreMissingError naming %s", err, missing)
	}
	for _, want := range []string{missing, "nova-swarm slots init --store " + missing + " --owner <name>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("list refusal %q does not carry %q", err.Error(), want)
		}
	}

	released, held, live, err := ReleaseSlotLeasesForcing(missing, "alice", "", true, false)
	if !errors.As(err, &me) || released != 0 || held != 0 || live != 0 {
		t.Fatalf("release on a missing store = %d %d %d %v, want the refusal", released, held, live, err)
	}
	if !strings.Contains(err.Error(), "--owner alice") {
		t.Errorf("release refusal %q does not write the remedy for the owner it was given", err.Error())
	}
	if _, serr := os.Stat(missing); !os.IsNotExist(serr) {
		t.Errorf("the refusal made the missing store: %v", serr)
	}

	if _, _, _, _, _, _, err := TakeSlotLeases(missing, "alice", 1, time.Hour, "", now, os.Getpid()); !errors.As(err, &me) {
		t.Errorf("take on a missing store: err %v, want *SlotStoreMissingError", err)
	}

	// A store with shares.tsv and no slots/ yet is what init made and no
	// take has touched: empty, not missing.
	fresh := writeSlotStore(t, "capacity\t1\nreserve\t0\nalice\t1\n")
	if leases, err := ListSlotLeases(fresh, now); err != nil || len(leases) != 0 {
		t.Errorf("a fresh store lists %v %v, want empty and no error", leases, err)
	}
	if released, _, _, err := ReleaseSlotLeasesForcing(fresh, "alice", "", true, false); err != nil || released != 0 {
		t.Errorf("release on a fresh store = %d %v, want 0 and no error", released, err)
	}
}

// An unreadable lease directory is a CORRUPT row, never a skipped one: it
// is listed with its reason, it is not counted as anyone's seat, and it is
// left for the next take to reap.
func TestSlotListShowsACorruptLeaseAsARow(t *testing.T) {
	t.Parallel()
	store := writeSlotStore(t, "capacity\t3\nreserve\t0\nalice\t3\n")
	now := time.Now().UTC()
	if err := MakeSlotLease(store, "good-1", "alice", os.Getpid(), "card-1", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// One directory with no lease file, one with a lease file that is not a lease.
	for name, body := range map[string]*string{"bare-2": nil, "junk-3": strPtr("owner=alice\npid=not-a-number\n")} {
		dir := filepath.Join(store, "slots", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if body != nil {
			if err := os.WriteFile(filepath.Join(dir, "lease"), []byte(*body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	leases, err := ListSlotLeases(store, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 3 {
		t.Fatalf("list = %+v, want the good lease and two CORRUPT rows", leases)
	}
	rows := map[string]string{}
	for _, l := range leases {
		rows[l.ID] = l.Line(now)
	}
	if !strings.HasPrefix(rows["good-1"], "SLOT good-1 owner=alice ") || strings.Contains(rows["good-1"], "CORRUPT") {
		t.Errorf("the good lease's row: %s", rows["good-1"])
	}
	for id, why := range map[string]string{"bare-2": "no such file", "junk-3": "pid is not a number"} {
		if !strings.HasPrefix(rows[id], "SLOT "+id+" state=CORRUPT reason=") || !strings.Contains(rows[id], why) {
			t.Errorf("corrupt %s row %q does not say CORRUPT and why (%s)", id, rows[id], why)
		}
	}
	// Not counted: holdings and utilisation see one seat held.
	if held, share, err := SlotHoldings(store, "alice", now); err != nil || held != 1 || share != 3 {
		t.Errorf("holdings = %d/%d %v, want 1/3", held, share, err)
	}
	if _, _, held, free, _, _, err := SlotUtilisation(store, now); err != nil || held != 1 || free != 2 {
		t.Errorf("utilisation held=%d free=%d %v, want 1 and 2", held, free, err)
	}
	// The next take reaps them, as the row said it would.
	if ok, _, _, _, _, _ := mustSlotTake(t, store, "alice", 1, time.Hour, "", now, os.Getpid()); !ok {
		t.Fatal("the take was refused")
	}
	leases, err = ListSlotLeases(store, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range leases {
		if l.Corrupt != "" {
			t.Errorf("the take left the corrupt lease %s: %s", l.ID, l.Corrupt)
		}
	}
}

func strPtr(s string) *string { return &s }
