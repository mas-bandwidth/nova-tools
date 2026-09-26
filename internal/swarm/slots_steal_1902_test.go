package swarm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Giving back the seat you are sitting in is the ordinary end of a run, not a steal:
// run's dispatcher and native's cleanup both release a lease whose pid is this
// process's and is alive.
func TestAHolderMayStillReleaseItsOwnLiveSeat(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	writeShares1902(t, store, "capacity\t2\nreserve\t0\nbench\t2\n")
	if _, _, _, _, _, ok, err := TakeSlotLeases(store, "bench", 1, time.Hour, "card-7", time.Now().UTC(), os.Getpid()); err != nil || !ok {
		t.Fatalf("take: ok=%v err=%v", ok, err)
	}
	released, held, live, err := ReleaseSlotLeasesForcing(store, "bench", "card-7", false, false)
	if err != nil || released != 1 || held != 0 || live != 0 {
		t.Fatalf("a holder could not give back its own seat: released=%d held=%d live=%d err=%v", released, held, live, err)
	}
	if _, err := os.Stat(filepath.Join(store, "slots")); err != nil {
		t.Fatal(err)
	}
}

func writeShares1902(t *testing.T, store, body string) {
	t.Helper()
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "shares.tsv"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
