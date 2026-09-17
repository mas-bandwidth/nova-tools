//go:build !unix

package main

import "os"

// nativeSignals is the one stop every platform has; SIGALRM is a unix alarm and has no
// meaning here (issue #1129).
func nativeSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

// nativeSignalReason: the one stop this platform has is a manager's interrupt, the same
// ending SIGTERM names on unix.
func nativeSignalReason(s os.Signal) string { return "terminated" }
