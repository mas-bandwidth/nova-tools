package swarm

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// Concurrent takes must never reap each other's half-written lease directory.
// TakeSlotLeases scans the store and reaps every directory with no lease file,
// but a directory with no lease file is also a sibling take three microseconds
// old: Mkdir landed, WriteFile has not. When the reaper wins that window the
// victim's WriteFile fails on a parent that is gone and the card never runs.
// Each worker takes and releases in a loop so the store stays small, scans
// stay fast, and takes overlap constantly: the losing interleaving arrives
// within a few hundred takes.
func TestConcurrentTakesKeepEveryTake(t *testing.T) {
	store := writeSlotStore(t, "capacity\t64\nreserve\t0\nalice\t64\nbob\t64\n")
	now := time.Now().UTC()
	pid := os.Getpid()

	const workers = 8
	const perWorker = 150

	var wg sync.WaitGroup
	problems := make(chan string, workers*perWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			owner := "alice"
			if w%2 == 1 {
				owner = "bob"
			}
			for i := 0; i < perWorker; i++ {
				ids, _, _, _, _, ok, err := TakeSlotLeases(store, owner, 2, time.Hour, "race", now, pid)
				if err != nil {
					problems <- fmt.Sprintf("worker %d iter %d: TakeSlotLeases: %v", w, i, err)
					continue
				}
				if !ok || len(ids) != 2 {
					problems <- fmt.Sprintf("worker %d iter %d: refused with room to spare: ok=%v granted=%d", w, i, ok, len(ids))
					continue
				}
				for _, id := range ids {
					if _, rerr := readSlotLease(store, id); rerr != nil {
						problems <- fmt.Sprintf("worker %d iter %d: granted lease %s unreadable: a concurrent take reaped a half-written lease directory: %v", w, i, id, rerr)
					}
				}
				if _, rerr := ReleaseSlotLeasesByID(store, ids, pid); rerr != nil {
					problems <- fmt.Sprintf("worker %d iter %d: release: %v", w, i, rerr)
				}
			}
		}(w)
	}
	wg.Wait()
	close(problems)

	for p := range problems {
		t.Errorf("a concurrent take lost its lease directory mid-take: %s", p)
	}
	leases, err := ListSlotLeases(store, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 0 {
		t.Errorf("every take was released, so no lease may remain: %d left", len(leases))
	}
}
