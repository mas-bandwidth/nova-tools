//go:build !windows

package main

import (
	"errors"
	"syscall"
)

// processAlive says whether pid is a running process: signal 0 is delivered to
// nobody, and only says whether the process exists (EPERM: it does, and is
// another user's).
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
