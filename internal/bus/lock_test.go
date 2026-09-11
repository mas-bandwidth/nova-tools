package bus

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
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
	bare := bareBus(t)
	clone := cloneBus(t, bare)

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

	// The lock is per CHECKOUT and not per bus name: a second clone of one bus is a
	// second checkout and must not be blocked by the first.
	other := cloneBus(t, bare)
	held, err := LockCheckout(clone, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer held()
	elsewhere, err := LockCheckout(other, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("a second checkout of the same bus was blocked by the first: %v", err)
	}
	elsewhere()
	// And it lives in the git directory, where it is not a file on the bus that every
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

// LockFile can be called directly on any file path.
func TestLockFileNonBlockingAndHolderStamping(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")

	release, err := LockFile(lockPath, 0)
	if err != nil {
		t.Fatalf("first LockFile failed: %v", err)
	}
	defer release()

	// Verify holder was stamped with our PID
	holder := ReadLockHolder(lockPath)
	wantPID := strconv.Itoa(os.Getpid())
	if holder != wantPID {
		t.Fatalf("holder = %q, want %q", holder, wantPID)
	}

	// Second LockFile with wait=0 must fail immediately with ErrLockHeld
	start := time.Now()
	_, err2 := LockFile(lockPath, 0)
	if err2 == nil {
		t.Fatal("second LockFile with wait=0 succeeded, want ErrLockHeld")
	}
	if !errors.Is(err2, ErrLockHeld) {
		t.Fatalf("err = %v, want errors.Is(err, ErrLockHeld)", err2)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("LockFile with wait=0 took %v, want near-immediate return", elapsed)
	}

	// Release first lock, second should succeed
	release()
	release2, err3 := LockFile(lockPath, 100*time.Millisecond)
	if err3 != nil {
		t.Fatalf("LockFile after release failed: %v", err3)
	}
	defer release2()
}

func TestReadLockHolderFormats(t *testing.T) {
	dir := t.TempDir()

	// Missing file returns "-"
	if h := ReadLockHolder(filepath.Join(dir, "missing.lock")); h != "-" {
		t.Fatalf("missing file holder = %q, want \"-\"", h)
	}

	// Empty file returns "-"
	emptyPath := filepath.Join(dir, "empty.lock")
	if err := os.WriteFile(emptyPath, []byte("  \n"), 0644); err != nil {
		t.Fatal(err)
	}
	if h := ReadLockHolder(emptyPath); h != "-" {
		t.Fatalf("empty file holder = %q, want \"-\"", h)
	}

	// Bare PID returns the PID
	barePath := filepath.Join(dir, "bare.lock")
	if err := os.WriteFile(barePath, []byte("12345\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if h := ReadLockHolder(barePath); h != "12345" {
		t.Fatalf("bare PID holder = %q, want \"12345\"", h)
	}

	// "pid=<n> at=<stamp>" format returns the PID
	mergePath := filepath.Join(dir, "merge.lock")
	if err := os.WriteFile(mergePath, []byte("pid=67890 at=2026-09-11T12:00:00Z\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if h := ReadLockHolder(mergePath); h != "67890" {
		t.Fatalf("merge format holder = %q, want \"67890\"", h)
	}
}
