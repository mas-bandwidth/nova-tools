package friend

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

const ProcessIdentitySupported = true

// ProcessIdentity is a native process birth reading. A missing or zombie
// process has no adoptable identity.
func ProcessIdentity(pid int) string {
	if pid <= 0 {
		return ""
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil || kp.Proc.P_stat == 5 { // BSD SZOMB
		return ""
	}
	return fmt.Sprintf("%d:%d", kp.Proc.P_starttime.Sec, kp.Proc.P_starttime.Usec)
}

func ProcessGroupAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(-pid, 0)
	return err == nil || err == syscall.EPERM
}
