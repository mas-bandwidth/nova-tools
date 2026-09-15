//go:build darwin || linux

package sandbox

import (
	"os"
	"syscall"
)

// statusOf is the exit-status mapping of rule 12: the child's status is the tool's, and
// a death by signal N is 128+N. It is shared by the darwin and linux bodies, which both
// WAIT on a child and both owe the caller that mapping.
func statusOf(waitErr error, st *os.ProcessState) int {
	if st != nil {
		if ws, ok := st.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				return 128 + int(ws.Signal())
			}
			return ws.ExitStatus()
		}
		return st.ExitCode()
	}
	if waitErr != nil {
		return ExitNotExecuted
	}
	return 0
}
