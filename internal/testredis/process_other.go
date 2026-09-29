//go:build !unix

package testredis

import (
	"errors"
	"os"
)

// isProcessAlive reports whether a process with the given pid is running.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p
	return true
}

// killProcess kills the process.
func killProcess(pid int) error {
	if pid <= 0 {
		return errors.New("invalid pid")
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
