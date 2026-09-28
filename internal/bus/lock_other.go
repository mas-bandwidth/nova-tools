//go:build !unix && !windows

package bus

import (
	"errors"
	"os"
)

// The lock, where there is no flock and not Windows: generic exclusive create of a sibling file.
func tryLockFile(f *os.File) (bool, bool, error) {
	held, err := os.OpenFile(sentinel(f), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		return true, false, held.Close()
	}
	if errors.Is(err, os.ErrExist) {
		return false, true, nil
	}
	return false, false, err
}

// platformTransientLockCollision is false on platforms whose filesystem does not
// put a just-released file into a delete-pending state.
func platformTransientLockCollision(error) bool { return false }

// processAlive cannot be answered where there is no process signal, and this
// build would rather leave a stale sentinel for an operator than clear a lock
// whose holder it cannot prove is gone.
func processAlive(pid int) bool { return true }

func unlockFile(f *os.File) {
	_ = removeLockFile(sentinel(f))
}

// sentinel is the file whose existence means the lock is held, beside the lock file the
// caller opened.
func sentinel(f *os.File) string {
	return sentinelPath(f.Name())
}
