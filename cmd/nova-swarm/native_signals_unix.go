//go:build unix

package main

import (
	"os"
	"syscall"
)

// nativeSignals are the stops a native run honours beside its own --deadline (issue
// #1129): SIGTERM from a stop, and SIGALRM from the outer `alarm` an adoption pass wraps
// the probe in. Each ends the run the way the wall does -- kill the harness group, write
// the abstain with reason=deadline, exit 1 -- rather than being ignored while the harness
// hangs on a queued provider request.
func nativeSignals() []os.Signal {
	return []os.Signal{syscall.SIGTERM, syscall.SIGALRM}
}

// nativeSignalReason is the one reason token a stop from outside writes: a manager's
// SIGTERM is `terminated`, and SIGALRM is the adoption pass's outer alarm -- a deadline
// by another name -- so it is `deadline`.
func nativeSignalReason(s os.Signal) string {
	if s == syscall.SIGTERM {
		return "terminated"
	}
	return "deadline"
}
