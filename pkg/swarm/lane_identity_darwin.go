//go:build darwin

package swarm

import (
	"golang.org/x/sys/unix"
	"strconv"
)

// LaneStartStamp is the kernel birth time, not a file timestamp or a command name.
func LaneStartStamp(pid int) string {
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || p == nil {
		return "-"
	}
	return strconv.FormatInt(p.Proc.P_starttime.Sec, 10) + ":" + strconv.FormatInt(int64(p.Proc.P_starttime.Usec), 10)
}

func laneMarked(pid int, mark string) bool {
	return procargsEnvHolds(pid, []byte(LaneEnv+"="+mark))
}
