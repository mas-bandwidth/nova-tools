//go:build !unix

package swarm

import (
	"fmt"
	"os"
	"time"
)

// takeKernelLock where there is no flock: lock_other.go's exclusive create of a sibling
// file, polled until deadline. This is the loop every platform ran before the unix build
// moved onto internal/filelock; it stays as it was until a migration in which an old and
// a new binary on one machine still exclude each other (the old lock is the sibling
// file, filelock's is LockFileEx on the file itself).
func takeKernelLock(path string, deadline time.Time) (func(), bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, false, fmt.Errorf("the lock at %s could not be opened: %w", path, err)
	}
	for {
		ok, lockErr := tryLockFile(f)
		if lockErr != nil {
			f.Close()
			return nil, false, fmt.Errorf("the lock at %s could not be taken: %w", path, lockErr)
		}
		if ok {
			return func() {
				unlockFile(f)
				f.Close()
			}, false, nil
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, true, nil
		}
		time.Sleep(lockPoll)
	}
}

const lockPoll = 15 * time.Millisecond
