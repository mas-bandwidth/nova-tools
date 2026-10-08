package main

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// markInheritedFDsCloseOnExec marks descriptors from the invoking shell.
// functional-run passes only standard streams to its children; an inherited
// flock descriptor otherwise survives through go test into a Redis server.
func markInheritedFDsCloseOnExec() error {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return fmt.Errorf("list /proc/self/fd: %w", err)
	}
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil || fd <= 2 {
			continue
		}
		syscall.CloseOnExec(fd)
	}
	return nil
}
