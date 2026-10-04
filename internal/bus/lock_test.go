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
//
// The second run reaches the lock while the first holds it and parks in the injected
// clock's Sleep instead of polling the wall clock. The first run holds the lock until
// that parked signal arrives, so the whole test waits on events and spends no wall time
// (Glenn's rule; nova-tools #4221).
func TestTwoConcurrentRunsSerialiseOnOneCheckout(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)

	var mu sync.Mutex
	inside := 0
	most := 0

	// The first run takes the lock for real and holds it.
	release, err := LockCheckout(clone, 5*time.Second)
	require.NoError(t, err, "the first run could not take the lock: %v", err)
	mu.Lock()
	inside++
	if inside > most {
		most = inside
	}
	mu.Unlock()

	// The second run reaches for the lock; it finds it held and parks in the
	// clock's Sleep, which signals the test that the wait is real.
	clk := newSignalClock()
	entered := make(chan struct{})
	var wg sync.WaitGroup
	var secondErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		rel, err := lockCheckoutAt(clone, time.Second, clk)
		if err != nil {
			secondErr = err
			return
		}
		defer rel()
		close(entered)
		mu.Lock()
		inside++
		if inside > most {
			most = inside
		}
		mu.Unlock()
		mu.Lock()
		inside--
		mu.Unlock()
	}()

	// The second run is parked on the held lock, never inside it.
	select {
	case <-clk.slept:
	case <-entered:
		require.FailNow(t, "the second run took the lock while the first held it")
	}
	mu.Lock()
	in := inside
	mu.Unlock()
	require.Equal(t, 1, in, "the second run was inside while the first held the lock")

	// Let the first run go; the second then takes the lock.
	mu.Lock()
	inside--
	mu.Unlock()
	release()
	close(clk.gate)
	wg.Wait()
	require.NoError(t, secondErr, "the second run could not take the lock after the first let go: %v", secondErr)
	require.Equal(t, 1, most, "%d runs were inside the lock at once, want 1", most)
}

// signalClock is the lock's clock for a test that must know the wait has parked:
// the first Sleep announces itself and blocks until the test opens gate, so the
// second run reaches the lock without spending wall time.
type signalClock struct {
	now   time.Time
	slept chan struct{}
	gate  chan struct{}
	once  sync.Once
}

func newSignalClock() *signalClock {
	return &signalClock{
		now:   time.Unix(1_700_000_000, 0),
		slept: make(chan struct{}),
		gate:  make(chan struct{}),
	}
}

func (c *signalClock) Now() time.Time { return c.now }

func (c *signalClock) Sleep(d time.Duration) {
	c.now = c.now.Add(d)
	c.once.Do(func() { close(c.slept) })
	<-c.gate
}

// The checkout lock stamps its holder's pid into the lock file, a take with wait=0 fails
// at once with ErrLockHeld and never consults the clock, and once the holder lets go the
// lock is taken again.
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
	holder := ReadLockHolder(lockPath)
	wantPID := strconv.Itoa(os.Getpid())
	require.Equal(t, wantPID, holder, "holder = %q, want %q", holder, wantPID)

	// A second take with wait=0 must fail immediately with ErrLockHeld, and must not
	// consult the clock at all: the fake records whether it slept.
	clk := newLockStepClock()
	_, err2 := lockFile(lockPath, 0, tryLockFile, clk)
	require.False(t, err2 == nil, "a second take with wait=0 succeeded, want ErrLockHeld")
	require.True(t, errors.Is(err2, ErrLockHeld), "err = %v, want errors.Is(err, ErrLockHeld)", err2)
	{
		waited := clk.waited()
		require.Equal(t, time.Duration(0), waited, "a take with wait=0 waited %v, want near-immediate return", waited)
	}

	// Release the lock; the next take succeeds.
	release()
	release2, err3 := LockCheckout(clone, time.Second)
	require.Equal(t, nil, err3, "the take after release failed: %v", err3)
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
