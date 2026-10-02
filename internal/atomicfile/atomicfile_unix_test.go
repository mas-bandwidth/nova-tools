//go:build !windows

package atomicfile

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUmaskHonored(t *testing.T) {
	t.Parallel()

	if os.Getenv("GO_TEST_SUBPROCESS_UMASK") == "077" {
		syscall.Umask(0o077)
		dir := t.TempDir()
		target := filepath.Join(dir, "umask_077_test.txt")

		err := Write(target, []byte("private content\n"), 0o644)
		require.NoError(t, err, "Write failed: %v", err)

		info, err := os.Stat(target)
		require.NoError(t, err, "Stat failed: %v", err)

		require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "file perm with umask 077 = %04o, want 0600 (umask was ignored!)", info.Mode().Perm())
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestUmaskHonored$")
	cmd.Env = append(os.Environ(), "GO_TEST_SUBPROCESS_UMASK=077")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "subprocess failed: %v\n%s", err, string(out))
}

func TestUmaskGroupWritableHonored(t *testing.T) {
	t.Parallel()

	if os.Getenv("GO_TEST_SUBPROCESS_UMASK") == "002" {
		syscall.Umask(0o002)
		dir := t.TempDir()
		target := filepath.Join(dir, "umask_002_test.txt")

		err := Write(target, []byte("group content\n"), 0o666)
		require.NoError(t, err, "Write failed: %v", err)

		info, err := os.Stat(target)
		require.NoError(t, err, "Stat failed: %v", err)

		require.Equal(t, os.FileMode(0o664), info.Mode().Perm(), "file perm with umask 002 = %04o, want 0664 (umask was ignored!)", info.Mode().Perm())
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestUmaskGroupWritableHonored$")
	cmd.Env = append(os.Environ(), "GO_TEST_SUBPROCESS_UMASK=002")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "subprocess failed: %v\n%s", err, string(out))
}

func TestExactModeBypassesUmask(t *testing.T) {
	t.Parallel()

	if os.Getenv("GO_TEST_SUBPROCESS_EXACT_MODE") == "077" {
		syscall.Umask(0o077)
		dir := t.TempDir()
		target := filepath.Join(dir, "exact_mode_077_test.txt")

		err := Write(target, []byte("exact content\n"), 0o644, ExactMode())
		require.NoError(t, err, "Write with ExactMode failed: %v", err)

		info, err := os.Stat(target)
		require.NoError(t, err, "Stat failed: %v", err)

		require.Equal(t, os.FileMode(0o644), info.Mode().Perm(), "file perm with ExactMode under umask 077 = %04o, want 0644", info.Mode().Perm())
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestExactModeBypassesUmask$")
	cmd.Env = append(os.Environ(), "GO_TEST_SUBPROCESS_EXACT_MODE=077")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "subprocess failed: %v\n%s", err, string(out))
}
