//go:build !windows

package fuse

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// TestPreserveUnreadablePreservesModeUnderUmask asserts that an existing 0600 destination
// is not widened to 0644 by PreserveUnreadable under an ordinary umask 022, and that a
// newly created unreadable backup receives 0644 subject to umask.
// The umask is isolated in a selected child process so other parallel tests are not affected.
func TestPreserveUnreadablePreservesModeUnderUmask(t *testing.T) {
	t.Parallel()

	if os.Getenv("GO_TEST_SUBPROCESS_PRESERVE_UMASK") == "022" {
		syscall.Umask(0o022)
		dir := t.TempDir()
		path := filepath.Join(dir, "fuses.json")
		if err := os.WriteFile(path, []byte(`{"lockdown":{"corrupt":`), 0o644); err != nil {
			t.Fatalf("write box: %v", err)
		}

		dst := path + UnreadableSuffix
		if err := os.WriteFile(dst, []byte("existing backup\n"), 0o600); err != nil {
			t.Fatalf("write dst: %v", err)
		}
		if err := os.Chmod(dst, 0o600); err != nil {
			t.Fatalf("chmod dst: %v", err)
		}

		if _, err := PreserveUnreadable(path); err != nil {
			t.Fatalf("PreserveUnreadable: %v", err)
		}

		fi, err := os.Stat(dst)
		if err != nil {
			t.Fatalf("stat dst: %v", err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Fatalf("destination mode under umask 022 = %04o, want 0600 (mode was widened!)", perm)
		}

		// Newly created destination gets 0644 under umask 022.
		path2 := filepath.Join(dir, "fuses2.json")
		if err := os.WriteFile(path2, []byte(`{"lockdown":{"corrupt2":`), 0o644); err != nil {
			t.Fatalf("write box2: %v", err)
		}
		dst2, err := PreserveUnreadable(path2)
		if err != nil {
			t.Fatalf("PreserveUnreadable 2: %v", err)
		}
		fi2, err := os.Stat(dst2)
		if err != nil {
			t.Fatalf("stat dst2: %v", err)
		}
		if perm := fi2.Mode().Perm(); perm != 0o644 {
			t.Fatalf("new destination mode under umask 022 = %04o, want 0644", perm)
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestPreserveUnreadablePreservesModeUnderUmask$")
	cmd.Env = append(os.Environ(), "GO_TEST_SUBPROCESS_PRESERVE_UMASK=022")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess failed: %v\n%s", err, string(out))
	}
}

// TestWriteBoxHonorsRestrictiveUmask asserts that WriteBox creates files honoring
// restrictive process umask (e.g. 077), yielding 0600 permissions instead of
// unconditionally forcing 0644 via chmod.
// The umask is isolated in a selected child process so other parallel tests are not affected.
func TestWriteBoxHonorsRestrictiveUmask(t *testing.T) {
	t.Parallel()

	if os.Getenv("GO_TEST_SUBPROCESS_WRITEBOX_UMASK") == "077" {
		syscall.Umask(0o077)
		dir := t.TempDir()
		path := filepath.Join(dir, "fuses.json")

		if err := WriteBox(path, Box{}); err != nil {
			t.Fatalf("WriteBox failed: %v", err)
		}

		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat box: %v", err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Fatalf("box perm with umask 077 = %04o, want 0600 (umask was ignored!)", perm)
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestWriteBoxHonorsRestrictiveUmask$")
	cmd.Env = append(os.Environ(), "GO_TEST_SUBPROCESS_WRITEBOX_UMASK=077")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess failed: %v\n%s", err, string(out))
	}
}
