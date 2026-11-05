package main

import (
	"os"
	"strconv"
)

// parentCommand is the parent's command line from /proc, or "" if unreadable.
func parentCommand() string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(os.Getppid()) + "/cmdline")
	if err != nil {
		return ""
	}
	return joinArgs(b)
}
