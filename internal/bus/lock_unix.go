//go:build unix

package bus

import (
	"errors"
	"os"
	"syscall"
)

// tryLockFile takes an exclusive advisory lock without blocking, and reports whether it
// got one. EWOULDBLOCK is the answer NO rather than a failure: somebody else holds it.
func tryLockFile(f *os.File) (bool, bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, false, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EACCES) {
		return false, true, nil
	}
	return false, false, err
}

func unlockFile(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// platformTransientLockCollision is false on unix: the filesystem does not put a
// just-released file into a delete-pending state, so a removal that fails is a
// real failure rather than a collision to wait out.
func platformTransientLockCollision(error) bool { return false }

// processAlive reports whether pid names a running process. Signal 0 performs the
// permission and existence checks without delivering anything; EPERM means the
// process exists but belongs to somebody else, which is still alive for the
// purpose of not clearing its lock.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}
