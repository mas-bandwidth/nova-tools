//go:build !windows

package cardcontract

import (
	"syscall"
	"testing"
)

// plantFIFO makes a named pipe at path. syscall.Mkfifo does not exist on Windows, so the
// helper stands in a build-tagged file of its own, the way pkg/swarm's does.
func plantFIFO(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Skipf("this platform will not make a FIFO: %v", err)
	}
}
