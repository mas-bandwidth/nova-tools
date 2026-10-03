//go:build !unix

package swarm

import (
	"fmt"
	"os"
	"time"
)

// takeKernelLock uses lock_other.go's exclusive sibling-file create on platforms without
// flock. It polls until the deadline so concurrent processes continue to exclude each other.
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
