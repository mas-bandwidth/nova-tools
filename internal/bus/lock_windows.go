//go:build windows

package bus

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// The lock on Windows, where there is no flock in the standard library.
//
// The advisory lock the unix build uses has no standard-library equivalent here --
// LockFileEx lives in golang.org/x/sys and this repo is standard library only -- so the
// lock is an EXCLUSIVE CREATE of a sibling file (.held). It gives the same mutual exclusion
// and it gives up the one property that makes flock better: the kernel does not drop it
// when the process dies, so a run killed while holding it leaves the file behind and the
// next run waits its whole budget and then refuses, naming the file.
//
// TRANSIENT COLLISIONS ON WINDOWS:
// When an existing lock holder releases the lock, unlockFile calls os.Remove(sentinel(f)).
// Under Windows NTFS, deletion is asynchronous: DeleteFileW marks the file as
// STATUS_DELETE_PENDING while handles or directory table entries resolve.
// A concurrent waiter calling os.OpenFile(..., O_CREATE|O_EXCL) while deletion is pending
// receives ERROR_ACCESS_DENIED (5) or ERROR_SHARING_VIOLATION (32).
//
// These are transient collisions during lock handover, not permanent permission denials
// or live lock holders. If wait > 0, tryLockFile marks them retryable so LockFile can wait
// out the handover window; if the denial persists until the budget expires (e.g. genuine
// permission restriction on .held), LockFile preserves and returns the real access denied
// error rather than falsely claiming another process holds the lock.

const (
	errorAccessDenied     = syscall.Errno(5)
	errorSharingViolation = syscall.Errno(32)
	errorLockViolation    = syscall.Errno(33)
)

func platformTransientLockCollision(err error) bool {
	return errors.Is(err, errorAccessDenied) ||
		errors.Is(err, errorSharingViolation) ||
		errors.Is(err, errorLockViolation)
}

func tryLockFile(f *os.File) (bool, bool, error) {
	held, openErr := os.OpenFile(sentinel(f), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if openErr == nil {
		return true, false, held.Close()
	}
	if errors.Is(openErr, os.ErrExist) {
		// Clean lock contention: another process created the sentinel and holds the lock.
		return false, true, nil
	}
	if platformTransientLockCollision(openErr) {
		// Transient collision during lock release (e.g. DELETE_PENDING or sharing violation).
		// Retryable if caller has a wait budget, but preserved as real error if persistent.
		return false, true, openErr
	}
	return false, false, openErr
}

func unlockFile(f *os.File) {
	_ = os.Remove(sentinel(f))
}

func sentinel(f *os.File) string {
	return filepath.Clean(f.Name()) + ".held"
}
