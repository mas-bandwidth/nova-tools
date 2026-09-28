//go:build unix

package capacity

import (
	"errors"
	"syscall"
	"time"
)

// reapGroup confirms a quarantined debit's process group is gone: kill -0
// -<pgid> answering ESRCH is gone; a live group is SIGKILLed (5.1) and
// polled briefly for ESRCH. Anything else is unconfirmed.
func reapGroup(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return true
	}
	if err != nil {
		return false
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	for i := 0; i < 10; i++ {
		time.Sleep(10 * time.Millisecond)
		if kerr := syscall.Kill(-pgid, 0); errors.Is(kerr, syscall.ESRCH) {
			return true
		}
	}
	return false
}
