//go:build darwin

package main

import "golang.org/x/sys/unix"

const (
	prioDarwinProcess = 4
	prioDarwinBG      = 0x1000
)

func lowerPriority() error {
	if err := unix.Setpriority(prioDarwinProcess, 0, prioDarwinBG); err == nil {
		return nil
	}
	return unix.Setpriority(unix.PRIO_PROCESS, 0, 19)
}

func hostMemoryBytes() uint64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return n
}

func parentPID(pid int) int {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil {
		return 0
	}
	return int(kp.Eproc.Ppid)
}
