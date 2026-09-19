package swarm

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// TestConcurrentTakesNeverGrantMoreThanTheShare drives 64 goroutines at one
// store with a single owner whose share is 8, and asserts the take path grants
// exactly 8 of the 64 and refuses the other 56 with the SAME final-state line.
// The read-count-write in TakeSlotLeases must be serialised: without a lock two
// takers can read the same pre-grant count, both pass the check and both grant,
// so granted lands above 8 and the refusals scatter inconsistent held=/free=.
//
// The refusal line is the verbatim format of cmd/nova-swarm/slots.go:151, so it
// is owned there, not invented here.
func TestConcurrentTakesNeverGrantMoreThanTheShare(t *testing.T) {
	store := writeSlotStore(t, "capacity\t8\nreserve\t0\nracer\t8\n")
	now := time.Now().UTC()
	pid := os.Getpid()

	const racers = 64
	type result struct {
		ids     []string
		held    int
		share   int
		free    int
		holders string
		ok      bool
		err     error
	}
	results := make([]result, racers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			ids, held, share, free, holders, ok, err := TakeSlotLeases(store, "racer", 1, time.Hour, "", now, pid)
			results[i] = result{ids, held, share, free, holders, ok, err}
		}(i)
	}
	close(start)
	wg.Wait()

	granted := 0
	for i, r := range results {
		if r.err != nil {
			t.Fatalf("goroutine %d: TakeSlotLeases error: %v", i, r.err)
		}
		if r.ok {
			granted++
			continue
		}
		got := fmt.Sprintf("SLOTS REFUSED owner=%s want=%d held=%d share=%d free=%d holders=%s",
			"racer", 1, r.held, r.share, r.free, r.holders)
		want := "SLOTS REFUSED owner=racer want=1 held=8 share=8 free=0 holders=racer:8"
		if got != want {
			t.Fatalf("goroutine %d: refusal line %q, want %q", i, got, want)
		}
	}
	if granted != 8 {
		t.Fatalf("granted=%d, want 8", granted)
	}
}
