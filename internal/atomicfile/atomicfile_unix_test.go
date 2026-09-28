//go:build !windows

package atomicfile

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestUmaskHonored(t *testing.T) {
	t.Parallel()

	if os.Getenv("GO_TEST_SUBPROCESS_UMASK") == "077" {
		syscall.Umask(0o077)
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
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestUmaskHonored$")
	cmd.Env = append(os.Environ(), "GO_TEST_SUBPROCESS_UMASK=077")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess failed: %v\n%s", err, string(out))
	}
}

func TestUmaskGroupWritableHonored(t *testing.T) {
	t.Parallel()

	if os.Getenv("GO_TEST_SUBPROCESS_UMASK") == "002" {
		syscall.Umask(0o002)
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
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestUmaskGroupWritableHonored$")
	cmd.Env = append(os.Environ(), "GO_TEST_SUBPROCESS_UMASK=002")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess failed: %v\n%s", err, string(out))
	}
}
