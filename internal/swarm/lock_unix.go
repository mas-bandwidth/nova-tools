//go:build unix

package swarm

import (
	"errors"
	"os"
	"syscall"
)

// tryLockFile takes an exclusive advisory lock without blocking. EWOULDBLOCK is the answer
// NO rather than a failure: somebody else holds it. The kernel drops the lock when the
// holder dies however it dies, which is the whole reason it is an flock and not a file
// whose existence means "held" -- a dispatcher killed with the pool lock taken leaves
// nothing for the next one to clear, and that is the case rule 17 is about.
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
