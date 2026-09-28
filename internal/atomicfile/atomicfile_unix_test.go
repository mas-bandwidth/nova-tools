//go:build !windows

package atomicfile

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestUmaskHonored(t *testing.T) {
	// Set strict umask 0077: Write with perm 0644 must produce 0600 on disk.
	// This directly verifies that atomicfile lets the process umask apply naturally
	// and does not override it via explicit chmod to 0644.
	oldMask := syscall.Umask(0o077)
	defer syscall.Umask(oldMask)

	dir := t.TempDir()
	target := filepath.Join(dir, "umask_077_test.txt")

	if err := Write(target, []byte("private content\n"), 0o644); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file perm with umask 077 = %04o, want 0600 (umask was ignored!)", perm)
	}
}

func TestUmaskGroupWritableHonored(t *testing.T) {
	// Set umask 0002: Write with perm 0666 must produce 0664 on disk.
	oldMask := syscall.Umask(0o002)
	defer syscall.Umask(oldMask)

	dir := t.TempDir()
	target := filepath.Join(dir, "umask_002_test.txt")

	if err := Write(target, []byte("group content\n"), 0o666); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	if perm := info.Mode().Perm(); perm != 0o664 {
		t.Fatalf("file perm with umask 002 = %04o, want 0664 (umask was ignored!)", perm)
	}
}
