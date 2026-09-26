//go:build unix

package main

import (
	"errors"
	"syscall"
)

// pidAlive is kill(pid, 0): the process exists (EPERM: it does, another
// user's).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
