//go:build unix

package main

import "syscall"

// signalPid asks the pid to exit. It is the production signal of disk-guard's
// stop floor. Tests of the guard pass their own func.
func signalPid(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

// pidAlive reports whether pid is a live process.
func pidAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
