package bus

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/filelock"
	"github.com/stretchr/testify/require"
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

	// The second take waits out its budget before refusing: the run it is waiting for is
	// usually a fetch away from finishing. The bound below is a floor, never a ceiling --
	// a loaded machine may take longer, and only a run that gave up early is wrong.
	started := time.Now()
	if _, err := LockCheckout(clone, 200*time.Millisecond); err == nil {
		require.FailNow(t, "two runs took one checkout's lock at once; they would write one OPEN list between them")
	} else {
		for _, want := range []string{"another nova-bus is already running on this checkout", LockName, "run this again when that one has finished"} {
			require.Contains(t, err.Error(), want, "the refusal does not say %q: %v", want, err)
		}
		if strings.Contains(err.Error(), "\n") {
			require.NotContains(t, err.Error(), "\n", "the refusal is more than one line: %q", err.Error())
		}
	}
	took := time.Since(started)
	require.GreaterOrEqual(t, took, 150*time.Millisecond, "the second run gave up after %s of a 200ms budget", took)

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
// working beside it, and both eventually run. The first run is still holding the lock when
// the second's take starts -- the handoff channel is read before the take is left -- so the
// second's take is a wait for the first and not a fresh take after it let go.
func TestTwoConcurrentRunsSerialiseOnOneCheckout(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)

	first, err := LockCheckout(clone, time.Hour)
	require.NoError(t, err, "the first run could not take the lock: %v", err)

	var mu sync.Mutex
	inside := 0
	most := 0
	errs := make([]error, 1)
	var wg sync.WaitGroup
	tried := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(tried)
		second, err := LockCheckout(clone, time.Hour)
		if err != nil {
			errs[0] = err
			return
		}
		defer second()
		mu.Lock()
		inside++
		if inside > most {
			most = inside
		}
		mu.Unlock()
	}()
	<-tried
	mu.Lock()
	inside++
	if inside > most {
		most = inside
	}
	mu.Unlock()
	first()
	mu.Lock()
	inside--
	mu.Unlock()
	wg.Wait()
	require.NoError(t, errs[0], "the second run's take failed: %v", errs[0])
	require.Equal(t, 1, most, "%d runs were inside the lock at once, want 1", most)
}

// The checkout lock stamps its holder's pid into the lock file, a take with wait=0 fails
// at once with ErrLockHeld without spending any of the wait budget, and once the holder
// lets go the lock is taken again.
func TestTheCheckoutLockStampsItsHolderAndAWaitZeroTakeNeverWaits(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)

	release, err := LockCheckout(clone, 0)
	require.NoError(t, err, "the first take failed: %v", err)
	defer release()

	// The holder is stamped with our PID, so a waiter and a refusal can name it.
	gd, err := GitDir(clone)
	require.NoError(t, err)
	lockPath := filepath.Join(gd, LockName)
	stamp, err := filelock.ReadStamp(lockPath)
	require.NoError(t, err, "the stamped lock file: %v", err)
	require.Equal(t, os.Getpid(), stamp.PID, "holder = %+v, want this process %d", stamp, os.Getpid())

	// A second take with wait=0 must fail immediately with ErrLockHeld, and the answer
	// names the wait it spent, which is none: the take never entered the budget's loop.
	_, err2 := filelock.Lock(lockPath, checkoutLockLabel, 0)
	require.False(t, err2 == nil, "a second take with wait=0 succeeded, want ErrLockHeld")
	require.True(t, errors.Is(err2, ErrLockHeld), "err = %v, want errors.Is(err, ErrLockHeld)", err2)
	{
		held, ok := filelock.AsHeldError(err2)
		require.True(t, ok, "err = %v, want a held answer that names the holder", err2)
		require.Equal(t, time.Duration(0), held.Wait, "a take with wait=0 spent %v of budget, want none", held.Wait)
	}

	// Release the lock; the next take succeeds.
	release()
	release2, err3 := LockCheckout(clone, time.Second)
	require.Equal(t, nil, err3, "the take after release failed: %v", err3)
	defer release2()
}
