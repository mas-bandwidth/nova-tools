//go:build unix

package deal

import (
	"errors"
	"syscall"
)

// processAlive asks the kernel with signal 0; EPERM is a live process this
// user may not signal.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
