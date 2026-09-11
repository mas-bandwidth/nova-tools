//go:build unix

package wake

import (
	"errors"
	"os"
	"syscall"
)

// tryLockFile takes an exclusive advisory lock without blocking, and reports
// whether it got one. EWOULDBLOCK is the answer NO rather than a failure.
//
// The kernel releases it when this process exits however it exits, which is the
// whole reason it is an flock and not a file whose existence means "held": a
// watch killed at its harness ceiling leaves nothing behind for the next one to
// clear, and a stale sentinel that wedges every later run is a worse failure
// than the race it prevents.
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

func unlockFile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
