package control

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const lockPoll = 15 * time.Millisecond

// takeCoordinatorLock acquires an exclusive holder-lifetime lock on lockPath,
// waiting up to wait or until ctx is cancelled. The release function is safe
// to call more than once. The kernel drops the lock when the holder dies.
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
			stampHolder(f)
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
			held := holderPID(lockPath)
			_ = f.Close()
			return nil, fmt.Errorf("%w: another coordinator holds %s (pid %s), waited %s", ErrLockTimeout, lockPath, held, wait)
		}

		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(lockPoll):
		}
	}
}

func stampHolder(f *os.File) {
	line := fmt.Sprintf("pid=%d at=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	if err := f.Truncate(0); err != nil {
		return
	}
	if _, err := f.Seek(0, 0); err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Sync()
}

func holderPID(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "-"
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return "-"
	}
	for _, tok := range strings.Fields(s) {
		if v, ok := strings.CutPrefix(tok, "pid="); ok {
			if _, err := strconv.Atoi(v); err == nil {
				return v
			}
		}
	}
	if _, err := strconv.Atoi(s); err == nil {
		return s
	}
	return "-"
}
