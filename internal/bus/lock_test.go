package bus

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// One nova-bus per checkout, both ways: the second concurrent run waits and then refuses
// with a sentence, and the same second run takes the lock the moment the first lets go.
//
// The wait is short here and ten seconds in the binary. What is being asserted is the
// behaviour and the sentence, not the number, which is a policy the caller passes in.
func TestASecondRunOnOneCheckoutWaitsThenRefuses(t *testing.T) {
	hermetic(t)
	bare := bareTable(t)
	clone := cloneTable(t, bare)

	release, err := LockCheckout(clone, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("the first run could not take the lock: %v", err)
	}

	start := time.Now()
	if _, err := LockCheckout(clone, 200*time.Millisecond); err == nil {
		t.Fatal("two runs took one checkout's lock at once; they would write one OPEN list between them")
	} else {
		for _, want := range []string{"another nova-bus is already running on this checkout", LockName, "run this again when that one has finished"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the refusal does not say %q: %v", want, err)
			}
		}
		if strings.Contains(err.Error(), "\n") {
			t.Fatalf("the refusal is more than one line: %q", err.Error())
		}
	}
	// It WAITED before refusing, rather than refusing the instant it found the lock held:
	// the run it is waiting for is usually a fetch away from finishing.
	if waited := time.Since(start); waited < 150*time.Millisecond {
		t.Fatalf("the second run gave up after %s of a 200ms budget", waited)
	}

	// The other way: once the first lets go, the second takes it.
	release()
	second, err := LockCheckout(clone, time.Second)
	if err != nil {
		t.Fatalf("the lock was not released: %v", err)
	}
	second()
	// Releasing twice is not an error, because a run releases through a defer and may also
	// have released on its own path out.
	release()

	// The lock is per CHECKOUT and not per table name: a second clone of one table is a
	// second checkout and must not be blocked by the first.
	other := cloneTable(t, bare)
	held, err := LockCheckout(clone, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer held()
	elsewhere, err := LockCheckout(other, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("a second checkout of the same table was blocked by the first: %v", err)
	}
	elsewhere()
	// And it lives in the git directory, where it is not a file on the table that every
	// reader would have to know is not a note.
	gd, err := GitDir(clone)
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(gd, LockName)); statErr != nil {
		t.Fatalf("the lock is not at %s: %v", filepath.Join(gd, LockName), statErr)
	}
}

// Two runs that genuinely race: whichever gets there second waits for the first rather than
// working beside it, and both eventually run.
func TestTwoConcurrentRunsSerialiseOnOneCheckout(t *testing.T) {
	hermetic(t)
	bare := bareTable(t)
	clone := cloneTable(t, bare)

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
