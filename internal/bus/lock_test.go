package bus

import (
	"errors"
	"github.com/stretchr/testify/require"
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
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)

	release, err := LockCheckout(clone, 200*time.Millisecond)
	require.NoError(t, err, "the first run could not take the lock: %v", err)

	clk := newLockStepClock()
	if _, err := lockCheckoutAt(clone, 200*time.Millisecond, clk); err == nil {
		require.FailNow(t, "two runs took one checkout's lock at once; they would write one OPEN list between them")
	} else {
		for _, want := range []string{"another nova-bus is already running on this checkout", LockName, "run this again when that one has finished"} {
			require.Contains(t, err.Error(), want, "the refusal does not say %q: %v", want, err)
		}
		if strings.Contains(err.Error(), "\n") {
			require.NotContains(t, err.Error(), "\n", "the refusal is more than one line: %q", err.Error())
		}
	}
	// It WAITED before refusing, rather than refusing the instant it found the lock held:
	// the run it is waiting for is usually a fetch away from finishing. The clock is the
	// test's, so the 200ms budget is measured in virtual time and costs no wall time.
	{
		waited := clk.waited()
		require.False(t, waited < 150*time.Millisecond, "the second run gave up after %s of virtual time of a 200ms budget", waited)
	}

	// The other way: once the first lets go, the second takes it.
	release()
	second, err := LockCheckout(clone, time.Second)
	require.NoError(t, err, "the lock was not released: %v", err)
	second()
	// Releasing twice is not an error, because a run releases through a defer and may also
	// have released on its own path out.
	release()

	// The lock is per CHECKOUT and not per bus name: a second clone of one bus is a
	// second checkout and must not be blocked by the first.
	other := cloneBus(t, bare)
	held, err := LockCheckout(clone, time.Second)
	require.NoError(t, err)
	defer held()
	elsewhere, err := LockCheckout(other, 200*time.Millisecond)
	require.NoError(t, err, "a second checkout of the same bus was blocked by the first: %v", err)
	elsewhere()
	// And it lives in the git directory, where it is not a file on the bus that every
	// reader would have to know is not a note.
	gd, err := GitDir(clone)
	require.NoError(t, err)
	{
		_, statErr := os.Stat(filepath.Join(gd, LockName))
		if statErr != nil {
			require.Equal(t, nil, statErr, "the lock is not at %s: %v", filepath.Join(gd, LockName), statErr)
		}
	}
}

// Two runs that genuinely race: whichever gets there second waits for the first rather than
// working beside it, and both eventually run.
func TestTwoConcurrentRunsSerialiseOnOneCheckout(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")
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
		require.NoError(t, err, "run %d: %v", i, err)
	}
	require.Equal(t, 1, most, "%d runs were inside the lock at once, want 1", most)
}

// LockFile can be called directly on any file path.
func TestLockFileNonBlockingAndHolderStamping(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")

	release, err := LockFile(lockPath, 0)
	require.NoError(t, err, "first LockFile failed: %v", err)
	defer release()

	// Verify holder was stamped with our PID
	holder := ReadLockHolder(lockPath)
	wantPID := strconv.Itoa(os.Getpid())
	require.Equal(t, wantPID, holder, "holder = %q, want %q", holder, wantPID)

	// Second LockFile with wait=0 must fail immediately with ErrLockHeld, and must not
	// consult the clock at all: the fake records whether it slept.
	clk := newLockStepClock()
	_, err2 := lockFile(lockPath, 0, tryLockFile, clk)
	require.False(t, err2 == nil, "second LockFile with wait=0 succeeded, want ErrLockHeld")
	require.True(t, errors.Is(err2, ErrLockHeld), "err = %v, want errors.Is(err, ErrLockHeld)", err2)
	{
		waited := clk.waited()
		require.Equal(t, time.Duration(0), waited, "LockFile with wait=0 waited %v, want near-immediate return", waited)
	}

	// Release first lock, second should succeed
	release()
	release2, err3 := LockFile(lockPath, 100*time.Millisecond)
	require.Equal(t, nil, err3, "LockFile after release failed: %v", err3)
	defer release2()
}

func TestReadLockHolderFormats(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Missing file returns "-"
	{
		h := ReadLockHolder(filepath.Join(dir, "missing.lock"))
		require.Equal(t, "-", h, "missing file holder = %q, want \"-\"", h)
	}

	// Empty file returns "-"
	emptyPath := filepath.Join(dir, "empty.lock")
	require.NoError(t, os.WriteFile(emptyPath, []byte("  \n"), 0644))
	{
		h := ReadLockHolder(emptyPath)
		require.Equal(t, "-", h, "empty file holder = %q, want \"-\"", h)
	}

	// Bare PID returns the PID
	barePath := filepath.Join(dir, "bare.lock")
	require.NoError(t, os.WriteFile(barePath, []byte("12345\n"), 0644))
	{
		h := ReadLockHolder(barePath)
		require.Equal(t, "12345", h, "bare PID holder = %q, want \"12345\"", h)
	}

	// "pid=<n> at=<stamp>" format returns the PID
	mergePath := filepath.Join(dir, "merge.lock")
	require.NoError(t, os.WriteFile(mergePath, []byte("pid=67890 at=2026-09-11T12:00:00Z\n"), 0644))
	{
		h := ReadLockHolder(mergePath)
		require.Equal(t, "67890", h, "merge format holder = %q, want \"67890\"", h)
	}
}
