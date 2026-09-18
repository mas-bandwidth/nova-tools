package bus

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// lockStepClock is the lock's clock in the tests: Now stands still until Sleep moves it,
// so the bounded wait reaches its deadline in as many polls as it would in real time and
// not one wall-clock millisecond. Production's clock is realLockClock.
type lockStepClock struct {
	start time.Time
	now   time.Time
}

func newLockStepClock() *lockStepClock {
	at := time.Unix(1_700_000_000, 0)
	return &lockStepClock{start: at, now: at}
}

func (c *lockStepClock) Now() time.Time        { return c.now }
func (c *lockStepClock) Sleep(d time.Duration) { c.now = c.now.Add(d) }
func (c *lockStepClock) waited() time.Duration { return c.now.Sub(c.start) }

// TestLockFileTransientCollisionRecoversWhenWaitBudgetAllows verifies that when tryLockFile
// encounters a transient collision during lock release/handover, lockFile waits out the window
// and successfully acquires the lock.
func TestLockFileTransientCollisionRecoversWhenWaitBudgetAllows(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")

	var polls int32
	try := func(f *os.File) (bool, bool, error) {
		if atomic.AddInt32(&polls, 1) < 3 {
			// Simulate transient collision (e.g. possible delete-pending or sharing contention)
			return false, true, syscall.Errno(5)
		}
		// On 3rd attempt, collision has cleared and lock is acquired
		return true, false, nil
	}

	clk := newLockStepClock()
	release, err := lockFile(lockPath, 500*time.Millisecond, try, clk)
	if err != nil {
		t.Fatalf("lockFile failed to recover from transient collision: %v", err)
	}
	defer release()

	if p := atomic.LoadInt32(&polls); p < 3 {
		t.Fatalf("lockFile acquired lock after %d polls, want at least 3", p)
	}
	if waited := clk.waited(); waited < 30*time.Millisecond {
		t.Fatalf("lockFile gave up after %v of virtual time, expected to wait for transient collision to clear", waited)
	}

	// Verify lock holder was stamped
	holder := ReadLockHolder(lockPath)
	if holder == "-" {
		t.Fatalf("ReadLockHolder returned %q, expected valid PID", holder)
	}
}

// TestLockFilePersistentCollisionPreservesActualErrorAndDoesNotFalselyAssertLockHeld verifies
// that when a collision error (such as permanent permission restriction on .held) persists,
// lockFile retries until the wait budget expires, never enters the critical section, and returns
// the real underlying error without falsely asserting ErrLockHeld or claiming another process is running.
func TestLockFilePersistentCollisionPreservesActualErrorAndDoesNotFalselyAssertLockHeld(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")

	const errAccessDenied = syscall.Errno(5)
	var polls int32
	try := func(f *os.File) (bool, bool, error) {
		atomic.AddInt32(&polls, 1)
		return false, true, errAccessDenied
	}

	clk := newLockStepClock()
	release, err := lockFile(lockPath, 60*time.Millisecond, try, clk)
	if err == nil {
		release()
		t.Fatal("lockFile succeeded despite permanent collision, want error")
	}

	// Must have waited out the budget, in virtual time the fake advanced.
	if waited := clk.waited(); waited < 50*time.Millisecond {
		t.Fatalf("lockFile aborted early after %v of virtual time, want at least 50ms budget", waited)
	}
	if p := atomic.LoadInt32(&polls); p < 2 {
		t.Fatalf("lockFile polled %d times, want multiple retries over budget", p)
	}

	// Must preserve the actual underlying error, NOT ErrLockHeld
	if !errors.Is(err, errAccessDenied) {
		t.Fatalf("err does not wrap expected access denied error (%v)", err)
	}
	if errors.Is(err, ErrLockHeld) {
		t.Fatalf("err wraps ErrLockHeld (%v); permanent failure must not falsely report lock held", err)
	}
	if strings.Contains(err.Error(), "is held by process") {
		t.Fatalf("err falsely asserts live process holder: %v", err)
	}
	if !strings.Contains(err.Error(), "the lock at") || !strings.Contains(err.Error(), "could not be taken") {
		t.Fatalf("err does not carry expected failure sentence: %v", err)
	}
}

// TestLockFileImmediateNonblockingRejectsCollisionImmediately verifies that with wait=0,
// a collision error returns immediately with the real error rather than waiting or claiming ErrLockHeld.
func TestLockFileImmediateNonblockingRejectsCollisionImmediately(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")

	const errAccessDenied = syscall.Errno(5)
	var attempts int32
	try := func(f *os.File) (bool, bool, error) {
		atomic.AddInt32(&attempts, 1)
		return false, true, errAccessDenied
	}

	clk := newLockStepClock()
	_, err := lockFile(lockPath, 0, try, clk)
	if err == nil {
		t.Fatal("lockFile with wait=0 succeeded on collision, want error")
	}

	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("lockFile with wait=0 called try %d times, want exactly 1 attempt", got)
	}
	if waited := clk.waited(); waited != 0 {
		t.Fatalf("lockFile with wait=0 waited %v, want an immediate return", waited)
	}

	// Real error preserved, not ErrLockHeld
	if !errors.Is(err, errAccessDenied) {
		t.Fatalf("err does not wrap expected access denied error (%v)", err)
	}
	if errors.Is(err, ErrLockHeld) {
		t.Fatalf("err wraps ErrLockHeld (%v); nonblocking collision must return real error", err)
	}
}

// TestLockFileImmediateNonblockingCleanContentionReturnsLockHeld verifies that with wait=0,
// when another run actually holds the lock (clean contention, no underlying error), lockFile
// returns ErrLockHeld immediately.
func TestLockFileImmediateNonblockingCleanContentionReturnsLockHeld(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")

	var attempts int32
	try := func(f *os.File) (bool, bool, error) {
		atomic.AddInt32(&attempts, 1)
		return false, true, nil // clean contention
	}

	clk := newLockStepClock()
	_, err := lockFile(lockPath, 0, try, clk)
	if err == nil {
		t.Fatal("lockFile with wait=0 succeeded on clean contention, want ErrLockHeld")
	}

	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("lockFile with wait=0 called try %d times, want exactly 1 attempt", got)
	}
	if waited := clk.waited(); waited != 0 {
		t.Fatalf("lockFile with wait=0 waited %v, want an immediate return", waited)
	}

	if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("err = %v, want ErrLockHeld", err)
	}
}

// TestLockFileNegativeControlMissingParentFailsAtOpen verifies that when the lock file
// cannot be opened because its parent directory does not exist, lockFile fails immediately
// at the primary OpenFile, never invoking the try operation.
func TestLockFileNegativeControlMissingParentFailsAtOpen(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "missing-dir", "test.lock")

	reachedTryLock := false
	try := func(f *os.File) (bool, bool, error) {
		reachedTryLock = true
		return true, false, nil
	}

	_, err := lockFile(lockPath, 50*time.Millisecond, try, newLockStepClock())
	if err == nil {
		t.Fatal("lockFile on missing directory succeeded, want error")
	}
	if reachedTryLock {
		t.Fatal("tryLockFile was reached despite missing parent directory")
	}
	if !strings.Contains(err.Error(), "could not be opened") {
		t.Fatalf("err %v did not fail at primary OpenFile", err)
	}
}
