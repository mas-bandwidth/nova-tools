//go:build darwin || linux

package yield

import (
	"errors"
	"syscall"
)

// alreadyBehind is whether a failed setpriority to n leaves the point made: the
// kernel refuses to raise the priority (EACCES or EPERM: an unprivileged process
// may not lower its nice) of a process or thread whose nice is already n or
// more. It is judged per process on darwin and per thread on Linux, never by one
// thread's reading for the rest; any other failure is a failure.
func alreadyBehind(err error, now, n int) bool {
	return (errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)) && now >= n
}
