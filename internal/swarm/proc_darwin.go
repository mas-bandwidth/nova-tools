//go:build darwin

package swarm

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// StartStamp identifies the live process by the kernel's birth time. A dead,
// zombie, or unreadable process has no signal authority.
func StartStamp(pid int) string {
	if pid <= 0 {
		return "-"
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil || kp.Proc.P_stat == 5 { // BSD SZOMB
		return "-"
	}
	return fmt.Sprintf("%d:%d", kp.Proc.P_starttime.Sec, kp.Proc.P_starttime.Usec)
}
