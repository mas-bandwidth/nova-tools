//go:build !windows

package check

import "syscall"

// syscallMkfifo makes a named pipe: an irregular file the audit used to skip.
func syscallMkfifo(path string) error { return syscall.Mkfifo(path, 0o644) }
