//go:build shippedsmoke && !windows

package shippedsmoke

import "syscall"

// makeFifo makes a named pipe: a file that is not a regular file at all.
func makeFifo(path string) error { return syscall.Mkfifo(path, 0o644) }
