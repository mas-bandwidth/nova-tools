//go:build functional && !linux && !windows

package main

import "syscall"

// alive says pid can be signalled; the test only uses it before the kill here.
func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }
