package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// THE RACES, TAKEN OUT: n instant-exit fake workers against n-1 slots. The slot FILES are
// the only authority on what is free, so allocation is ONE step -- find a free slot and
// reserve it, both under slots.lock -- and no slot number is ever held by two workers at the
// same instant. That is the 2026-09-10 `database is locked` failure arriving by a second
// route: two workers on one slot means one data home and one SQLite database.
func TestNoSlotIsEverHeldTwice(t *testing.T) {
	const n = 6 // n workers, n-1 allocatable slots
	dir := t.TempDir()
	p, err := OpenPool(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Hold the top slot for the whole test, so only 1..n-1 can ever be allocated.
	if err := p.Reserve(n, "held-for-the-test", filepath.Join(dir, "held"), "held-nonce", os.Getpid(), time.Now()); err != nil {
		t.Fatal(err)
	}
	jobDirFor := func(slot int) string { return filepath.Join(dir, "jobs", fmt.Sprintf("job-%d", slot)) }

	var (
		mu     sync.Mutex
		heldBy = map[int]int{}
		wg     sync.WaitGroup
	)
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				slot, err := p.claimFree(n, nil, fmt.Sprintf("job-%d-%d", w, i),
					fmt.Sprintf("nonce-%d-%d", w, i), os.Getpid(), time.Now(), jobDirFor)
				if err != nil {
					continue // the other workers hold every slot this instant
				}
				mu.Lock()
				if other, dup := heldBy[slot]; dup {
					t.Errorf("slot %d held by worker %d and worker %d at the same time", slot, other, w)
				}
				heldBy[slot] = w
				mu.Unlock()

				time.Sleep(500 * time.Microsecond) // the instant a worker holds its slot

				mu.Lock()
				delete(heldBy, slot)
				mu.Unlock()
				if err := p.Free(slot); err != nil {
					t.Errorf("releasing slot %d: %v", slot, err)
				}
			}
		}(w)
	}
	wg.Wait()
}
