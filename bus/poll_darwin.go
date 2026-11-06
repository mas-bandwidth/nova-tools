package main

import (
	"syscall"
	"unsafe"
)

// stdoutGone polls fd 1 without blocking (poll(2), the standard library
// has no wrapper): a pipe whose reader has gone reports POLLHUP or POLLERR,
// a closed fd POLLNVAL; a file or a terminal reports neither.
func stdoutGone() bool {
	p := pollFd{fd: 1, events: pollOut}
	_, _, e := syscall.Syscall(syscall.SYS_POLL, uintptr(unsafe.Pointer(&p)), 1, 0)
	return e == 0 && p.revents&pollGone != 0
}
