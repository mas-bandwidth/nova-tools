//go:build unix

package tokens

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestFoldLockRefusesFIFO(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	path := filepath.Join(out, LockName)
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	release, err := TakeFoldLock(out, 0)
	if release != nil {
		release()
	}
	if err == nil {
		t.Fatal("FIFO lock was accepted")
	}
	info, statErr := os.Lstat(path)
	if statErr != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("FIFO changed: %v (%v)", info, statErr)
	}
}
