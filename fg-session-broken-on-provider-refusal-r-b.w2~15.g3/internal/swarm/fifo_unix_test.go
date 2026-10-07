//go:build !windows

package swarm

import (
	"syscall"
	"testing"
)

// plantFIFO makes a named pipe at path. Certification 34771523657 at 9a95e33e: this
// helper lived in symlink_fifo_test.go and syscall.Mkfifo does not exist on Windows,
// so the whole package failed to build there and the FIFO tests never said so.
func plantFIFO(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Skipf("this platform will not make a FIFO: %v", err)
	}
}
