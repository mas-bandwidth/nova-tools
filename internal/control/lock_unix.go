//go:build unix

package control

import (
	"errors"
	"os"
	"syscall"
)

// tryLockFile attempts to acquire an exclusive, non-blocking advisory flock.
// Returns (true, nil) if acquired, (false, nil) if held by another process, or (false, err) on OS error.
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

// unlockFile releases the advisory flock on f.
func unlockFile(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
