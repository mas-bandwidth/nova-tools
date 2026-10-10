//go:build unix

package main

import (
	"runtime"
	"syscall"
)

// ownNice is this process's nice, by getpriority: darwin answers the nice itself, the
// raw Linux system call 20 minus it (internal/yield's threadNice says why).
func ownNice() (int, error) {
	raw, err := syscall.Getpriority(syscall.PRIO_PROCESS, 0)
	if err != nil {
		return 0, err
	}
	if runtime.GOOS == "linux" {
		return 20 - raw, nil
	}
	return raw, nil
}
