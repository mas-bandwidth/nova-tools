//go:build !unix && !windows

package control

import (
	"fmt"
	"os"
)

// tryLockFile refuses platforms without a holder-lifetime kernel lock in the
// standard library. A crash-stale .held sentinel is not a lock.
func tryLockFile(f *os.File) (bool, error) {
	return false, fmt.Errorf("%w: coordinator.lock requires a holder-lifetime kernel lock (flock or LockFileEx)", ErrUnsupportedLock)
}

func unlockFile(f *os.File) {}
