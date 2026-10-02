//go:build unix

package tokens

import (
	"errors"
	"os"
	"syscall"
)

// Nonblocking open lets the regular-file check refuse a replaced FIFO without
// waiting for a writer. O_NOFOLLOW refuses a final symlink at the open itself.
func openFoldLockFile(path string, flags int) (*os.File, error) {
	return os.OpenFile(path, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o644)
}

// tryLockFile takes an exclusive advisory lock without blocking. EWOULDBLOCK is the answer
// NO rather than a failure: somebody else holds it. The kernel releases it when this
// process exits however it exits, which is why a fold killed with the lock taken leaves
// nothing for the next fold to clear.
func tryLockFile(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EACCES) {
		return false, nil
	}
	return false, err
}

// ignored: unlock has no caller to report to; the lock is released when the descriptor closes
func unlockFile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
