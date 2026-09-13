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

// TestLockFileTransientCollisionRecoversWhenWaitBudgetAllows verifies that when tryLockFile
// encounters a transient collision (such as Windows delete-pending ERROR_ACCESS_DENIED) during
// lock release/handover, LockFile waits out the window and successfully acquires the lock.
func TestLockFileTransientCollisionRecoversWhenWaitBudgetAllows(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")

	var polls int32
	forceTryLockFile = func(f *os.File) (bool, bool, error) {
		if atomic.AddInt32(&polls, 1) < 3 {
			// Simulate transient Windows ERROR_ACCESS_DENIED during delete-pending
			return false, true, syscall.Errno(5)
		}
		// On 3rd attempt, delete-pending has cleared and lock is acquired
		return true, false, nil
	}
	defer func() { forceTryLockFile = nil }()

	start := time.Now()
	release, err := LockFile(lockPath, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("LockFile failed to recover from transient collision: %v", err)
	}
	defer release()

	if p := atomic.LoadInt32(&polls); p < 3 {
		t.Fatalf("LockFile acquired lock after %d polls, want at least 3", p)
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Fatalf("LockFile returned in %v, expected to wait for transient collision to clear", elapsed)
	}

	// Verify lock holder was stamped
	holder := ReadLockHolder(lockPath)
	if holder == "-" {
		t.Fatalf("ReadLockHolder returned %q, expected valid PID", holder)
	}
}

// TestLockFilePersistentCollisionPreservesActualErrorAndDoesNotFalselyAssertLockHeld verifies
// that when a collision error (such as permanent permission restriction on .held) persists,
// LockFile retries until the wait budget expires, never enters the critical section, and returns
// the real underlying error without falsely asserting ErrLockHeld or claiming another process is running.
func TestLockFilePersistentCollisionPreservesActualErrorAndDoesNotFalselyAssertLockHeld(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")

	const errAccessDenied = syscall.Errno(5)
	var polls int32
	forceTryLockFile = func(f *os.File) (bool, bool, error) {
		atomic.AddInt32(&polls, 1)
		return false, true, errAccessDenied
	}
	defer func() { forceTryLockFile = nil }()

	start := time.Now()
	release, err := LockFile(lockPath, 60*time.Millisecond)
	if err == nil {
		release()
		t.Fatal("LockFile succeeded despite permanent collision, want error")
	}

	// Must have waited out the budget
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Fatalf("LockFile aborted early after %v, want at least 50ms budget", elapsed)
	}
	if p := atomic.LoadInt32(&polls); p < 2 {
		t.Fatalf("LockFile polled %d times, want multiple retries over budget", p)
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
	forceTryLockFile = func(f *os.File) (bool, bool, error) {
		return false, true, errAccessDenied
	}
	defer func() { forceTryLockFile = nil }()

	start := time.Now()
	_, err := LockFile(lockPath, 0)
	if err == nil {
		t.Fatal("LockFile with wait=0 succeeded on collision, want error")
	}

	if elapsed := time.Since(start); elapsed > 40*time.Millisecond {
		t.Fatalf("LockFile with wait=0 took %v, want near-immediate return", elapsed)
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
// when another run actually holds the lock (clean contention, no underlying error), LockFile
// returns ErrLockHeld immediately.
func TestLockFileImmediateNonblockingCleanContentionReturnsLockHeld(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")

	forceTryLockFile = func(f *os.File) (bool, bool, error) {
		return false, true, nil // clean contention
	}
	defer func() { forceTryLockFile = nil }()

	start := time.Now()
	_, err := LockFile(lockPath, 0)
	if err == nil {
		t.Fatal("LockFile with wait=0 succeeded on clean contention, want ErrLockHeld")
	}

	if elapsed := time.Since(start); elapsed > 40*time.Millisecond {
		t.Fatalf("LockFile with wait=0 took %v, want near-immediate return", elapsed)
	}

	if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("err = %v, want ErrLockHeld", err)
	}
}

// TestLockFileNegativeControlUnwritablePathFailsAtOpen verifies that an unwritable lock path
// fails immediately at the primary file opening (line 56), never reaching tryLockFile.
func TestLockFileNegativeControlUnwritablePathFailsAtOpen(t *testing.T) {
	dir := t.TempDir()
	unwritableDir := filepath.Join(dir, "no-write")
	if err := os.Mkdir(unwritableDir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(unwritableDir, 0o755) }()

	reachedTryLock := false
	forceTryLockFile = func(f *os.File) (bool, bool, error) {
		reachedTryLock = true
		return true, false, nil
	}
	defer func() { forceTryLockFile = nil }()

	lockPath := filepath.Join(unwritableDir, "sub", "test.lock")
	_, err := LockFile(lockPath, 50*time.Millisecond)
	if err == nil {
		t.Fatal("LockFile on missing directory succeeded, want error")
	}
	if reachedTryLock {
		t.Fatal("tryLockFile was reached despite unwritable primary path")
	}
	if !strings.Contains(err.Error(), "could not be opened") {
		t.Fatalf("err %v did not fail at primary OpenFile", err)
	}
}
