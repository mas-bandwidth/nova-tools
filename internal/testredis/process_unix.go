//go:build unix

package testredis

import (
	"errors"
	"syscall"
)

// isProcessAlive reports whether a process with the given pid is running.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}

// killProcess sends SIGKILL to the process.
func killProcess(pid int) error {
	if pid <= 0 {
		return errors.New("invalid pid")
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}
