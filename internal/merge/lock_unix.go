//go:build unix

package merge

import (
	"errors"
	"os"
	"syscall"
)

// tryLockFile takes an exclusive advisory lock without blocking. EWOULDBLOCK is the
// answer NO rather than a failure: somebody else holds it.
//
// The kernel releases this when the process exits however it exits -- including SIGKILL,
// which is the case rule 2 is written for. Nothing is left behind for the next run to
// clear, so there is no stale rule to get wrong.
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
