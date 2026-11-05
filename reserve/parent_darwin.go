package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// parentCommand is the parent's command line from kern.procargs2, or "" if
// unreadable (another user's process).
func parentCommand() string {
	b, err := unix.SysctlRaw("kern.procargs2", os.Getppid())
	if err != nil {
		return ""
	}
	return procArgs(b)
}
