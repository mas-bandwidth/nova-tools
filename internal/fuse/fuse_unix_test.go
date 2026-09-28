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
// is not widened under umask 022, an existing 0640 destination is not narrowed
// under umask 077, and new backups receive 0644 subject to the current umask.
// The umask is isolated in a selected child process so other parallel tests are not affected.
func TestPreserveUnreadablePreservesModeUnderUmask(t *testing.T) {
	t.Parallel()

	if mask := os.Getenv("GO_TEST_SUBPROCESS_PRESERVE_UMASK"); mask == "022" || mask == "077" {
		wantExisting, wantNew := os.FileMode(0o600), os.FileMode(0o644)
		if mask == "077" {
			syscall.Umask(0o077)
			wantExisting, wantNew = 0o640, 0o600
		} else {
			syscall.Umask(0o022)
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "fuses.json")
		if err := os.WriteFile(path, []byte(`{"lockdown":{"corrupt":`), 0o644); err != nil {
			t.Fatalf("write box: %v", err)
		}

		dst := path + UnreadableSuffix
		if err := os.WriteFile(dst, []byte("existing backup\n"), 0o600); err != nil {
			t.Fatalf("write dst: %v", err)
		}
		if err := os.Chmod(dst, wantExisting); err != nil {
			t.Fatalf("chmod dst: %v", err)
		}

		if _, err := PreserveUnreadable(path); err != nil {
			t.Fatalf("PreserveUnreadable: %v", err)
		}

		fi, err := os.Stat(dst)
		if err != nil {
			t.Fatalf("stat dst: %v", err)
		}
		if perm := fi.Mode().Perm(); perm != wantExisting {
			t.Fatalf("destination mode under umask %s = %04o, want %04o", mask, perm, wantExisting)
		}

		// A newly created destination still honors the current umask.
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
		if perm := fi2.Mode().Perm(); perm != wantNew {
			t.Fatalf("new destination mode under umask %s = %04o, want %04o", mask, perm, wantNew)
		}
		return
	}

	for _, mask := range []string{"022", "077"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPreserveUnreadablePreservesModeUnderUmask$")
		cmd.Env = append(os.Environ(), "GO_TEST_SUBPROCESS_PRESERVE_UMASK="+mask)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("umask %s subprocess failed: %v\n%s", mask, err, string(out))
		}
	}
}

// TestWriteBoxUnderUmask077Is0644 asserts that WriteBox creates files with 0644
// permissions even under a restrictive process umask (e.g. 077) via atomicfile.ExactMode(),
// ensuring the fuse box remains readable across users and tools.
// The umask is isolated in a selected child process so other parallel tests are not affected.
func TestWriteBoxUnderUmask077Is0644(t *testing.T) {
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
		if perm := fi.Mode().Perm(); perm != 0o644 {
			t.Fatalf("box perm with umask 077 = %04o, want 0644", perm)
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestWriteBoxUnderUmask077Is0644$")
	cmd.Env = append(os.Environ(), "GO_TEST_SUBPROCESS_WRITEBOX_UMASK=077")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess failed: %v\n%s", err, string(out))
	}
}
