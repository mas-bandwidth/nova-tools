//go:build !windows

package main

import "syscall"

// replace becomes nova-worker, keeping the process image: the shim's stdin,
// stdout and stderr and its exit code are the tool's own. It never returns on
// success.
func replace(bin string, argv []string, env []string) (int, error) {
	return 0, syscall.Exec(bin, argv, env)
}
