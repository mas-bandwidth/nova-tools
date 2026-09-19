//go:build !windows

package pulse

import (
	"syscall"
	"testing"
)

// mkfifo makes RESULT.md a named pipe: a reader that opens it blocks until a writer
// arrives, and none ever does. A gate that finishes anyway never opened it.
func mkfifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("this platform cannot make a fifo: %v", err)
	}
}
