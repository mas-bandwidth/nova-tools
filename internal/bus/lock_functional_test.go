//go:build functional

package bus

import (
	"sync"
	"testing"
	"time"
)

// Two runs that genuinely race: whichever gets there second waits for the first rather than
// working beside it, and both eventually run.
func TestTwoConcurrentRunsSerialiseOnOneCheckout(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)

	var mu sync.Mutex
	inside := 0
	most := 0
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			release, err := LockCheckout(clone, 5*time.Second)
			if err != nil {
				errs[i] = err
				return
			}
			defer release()
			mu.Lock()
			inside++
			if inside > most {
				most = inside
			}
			mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			mu.Lock()
			inside--
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if most != 1 {
		t.Fatalf("%d runs were inside the lock at once, want 1", most)
	}
}
