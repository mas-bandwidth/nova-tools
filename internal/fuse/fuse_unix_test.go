//go:build !windows

package fuse

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
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
		err := os.WriteFile(path, []byte(`{"lockdown":{"corrupt":`), 0o644)
		require.NoError(t, err, "write box: %v", err)

		dst := path + UnreadableSuffix
		err = os.WriteFile(dst, []byte("existing backup\n"), 0o600)
		require.NoError(t, err, "write dst: %v", err)
		err = os.Chmod(dst, wantExisting)
		require.NoError(t, err, "chmod dst: %v", err)

		_, err = PreserveUnreadable(path)
		require.NoError(t, err, "PreserveUnreadable: %v", err)

		fi, err := os.Stat(dst)
		require.NoError(t, err, "stat dst: %v", err)
		perm := fi.Mode().Perm()
		require.Equal(t, wantExisting, perm, "destination mode under umask %s = %04o, want %04o", mask, perm, wantExisting)

		// A newly created destination still honors the current umask.
		path2 := filepath.Join(dir, "fuses2.json")
		err = os.WriteFile(path2, []byte(`{"lockdown":{"corrupt2":`), 0o644)
		require.NoError(t, err, "write box2: %v", err)
		dst2, err := PreserveUnreadable(path2)
		require.NoError(t, err, "PreserveUnreadable 2: %v", err)
		fi2, err := os.Stat(dst2)
		require.NoError(t, err, "stat dst2: %v", err)
		perm = fi2.Mode().Perm()
		require.Equal(t, wantNew, perm, "new destination mode under umask %s = %04o, want %04o", mask, perm, wantNew)
		return
	}

	for _, mask := range []string{"022", "077"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPreserveUnreadablePreservesModeUnderUmask$")
		cmd.Env = append(os.Environ(), "GO_TEST_SUBPROCESS_PRESERVE_UMASK="+mask)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "umask %s subprocess failed: %v\n%s", mask, err, string(out))
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

		err := WriteBox(path, Box{})
		require.NoError(t, err, "WriteBox failed: %v", err)

		fi, err := os.Stat(path)
		require.NoError(t, err, "stat box: %v", err)
		perm := fi.Mode().Perm()
		require.Equal(t, os.FileMode(0o644), perm, "box perm with umask 077 = %04o, want 0644", perm)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestWriteBoxUnderUmask077Is0644$")
	cmd.Env = append(os.Environ(), "GO_TEST_SUBPROCESS_WRITEBOX_UMASK=077")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "subprocess failed: %v\n%s", err, string(out))
}
