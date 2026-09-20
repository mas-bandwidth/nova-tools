package control

import (
	"context"
	"fmt"
	"os"
	"time"
)

const lockPoll = 15 * time.Millisecond

// takeCoordinatorLock acquires an exclusive flock on lockPath, waiting up to wait duration
// or until ctx is cancelled. It returns a release function that releases the lock and closes
// the underlying file. The release function is safe to call multiple times.
func takeCoordinatorLock(ctx context.Context, lockPath string, wait time.Duration) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("coordinator lock at %s could not be opened: %w", lockPath, err)
	}

	deadline := time.Now().Add(wait)
	for {
		if err := ctx.Err(); err != nil {
			_ = f.Close()
			return nil, err
		}

		ok, lockErr := tryLockFile(f)
		if lockErr != nil {
			_ = f.Close()
			return nil, fmt.Errorf("coordinator lock at %s could not be taken: %w", lockPath, lockErr)
		}
		if ok {
			released := false
			return func() {
				if released {
					return
				}
				released = true
				unlockFile(f)
				_ = f.Close()
			}, nil
		}

		if !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("%w: another coordinator holds %s, waited %s", ErrLockTimeout, lockPath, wait)
		}

		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(lockPoll):
		}
	}
}
