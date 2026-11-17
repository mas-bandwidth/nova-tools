//go:build freebsd || netbsd || openbsd || dragonfly

package main

import "golang.org/x/sys/unix"

func lowerPriority() error {
	return unix.Setpriority(unix.PRIO_PROCESS, 0, 19)
}

func hostMemoryBytes() uint64 {
	n, err := unix.SysctlUint64("hw.physmem")
	if err != nil {
		return 0
	}
	return n
}

func parentPID(int) int { return 0 }
