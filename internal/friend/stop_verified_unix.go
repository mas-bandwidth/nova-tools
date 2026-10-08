//go:build unix

package friend

import (
	"context"
	"syscall"
	"time"
)

// StopVerifiedRun signals only a process whose kernel birth still matches
// the durable receipt, then waits for every visible member of its group to
// exit. False means the caller must keep its return debt owed.
func StopVerifiedRun(ctx context.Context, pid int, identity string) bool {
	if identity == "" || ProcessIdentity(pid) != identity {
		return false
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		return false
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for ProcessGroupAlive(pid) {
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
	return true
}
