package main

import (
	"syscall"
	"unsafe"
)

// stdoutGone polls fd 1 without blocking (ppoll(2) with a zero timeout and
// no signal mask: every Linux architecture has it; the standard library has
// no wrapper): a pipe whose reader has gone reports POLLERR, a closed fd
// POLLNVAL; a file or a terminal reports neither.
func stdoutGone() bool {
	p := pollFd{fd: 1, events: pollOut}
	var zero syscall.Timespec
	_, _, e := syscall.Syscall6(syscall.SYS_PPOLL, uintptr(unsafe.Pointer(&p)), 1, uintptr(unsafe.Pointer(&zero)), 0, 0, 0)
	return e == 0 && p.revents&pollGone != 0
}
