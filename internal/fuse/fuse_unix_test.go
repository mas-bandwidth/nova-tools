//go:build darwin || linux

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

// TestWriteBoxRefusesASymlinkAncestorOwnedByAnotherThanRoot pins security
// finding 74.6: a user-owned symlink cannot redirect replacement or creation
// of the safety-control file into its target.
func TestWriteBoxRefusesASymlinkAncestorOwnedByAnotherThanRoot(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("the test needs a symlink owned by an identity other than root")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	require.NoError(t, os.MkdirAll(filepath.Join(target, "realdir"), 0o755))
	link := filepath.Join(dir, "linkdir")
	require.NoError(t, os.Symlink(target, link))

	existing := filepath.Join(target, "realdir", "box.json")
	require.NoError(t, os.WriteFile(existing, []byte("original\n"), 0o600))
	redirectedExisting := filepath.Join(link, "realdir", "box.json")
	require.Error(t, PlanWriteBox(redirectedExisting), "the write plan must refuse the same user-owned symlink ancestor")
	err := WriteBox(redirectedExisting, Box{})
	require.Error(t, err, "a user-owned symlink ancestor must be refused before replacing the box")
	got, readErr := os.ReadFile(existing)
	require.NoError(t, readErr)
	require.Equal(t, "original\n", string(got), "the target box changed through the symlink ancestor")

	redirectedNew := filepath.Join(link, "newdir", "box.json")
	require.Error(t, PlanCreateBox(redirectedNew), "the create plan must refuse before it predicts MkdirAll")
	err = CreateBox(redirectedNew)
	require.Error(t, err, "a user-owned symlink ancestor must be refused before creating directories or the box")
	_, statErr := os.Lstat(filepath.Join(target, "newdir"))
	require.True(t, os.IsNotExist(statErr), "CreateBox made a directory through the symlink ancestor: %v", statErr)
}

// TestWriteBoxAllowsOrdinaryPathAndRootOwnedAncestors pins the other side of
// the boundary: platform-owned path aliases (including macOS /var) do not
// prevent normal box creation under the test temporary directory.
func TestWriteBoxAllowsOrdinaryPathAndRootOwnedAncestors(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "box.json")
	require.NoError(t, PlanCreateBox(path))
	require.NoError(t, CreateBox(path))
	_, err := os.Stat(path)
	require.NoError(t, err)
}

// TestWriteBoxRefusesAUserOwnedSymlinkBehindARootOwnedAlias covers a root-owned
// alias whose immediate target is itself a user-owned symlink. The owner seam
// models the root-owned outer alias without requiring privileged test setup.
func TestWriteBoxRefusesAUserOwnedSymlinkBehindARootOwnedAlias(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	require.NoError(t, os.Mkdir(target, 0o755))
	inner := filepath.Join(dir, "user-link")
	require.NoError(t, os.Symlink(target, inner))
	outer := filepath.Join(dir, "root-alias")
	require.NoError(t, os.Symlink(inner, outer))

	owner := func(path string, info os.FileInfo) (uint32, bool) {
		if path == outer {
			return 0, true
		}
		if path == inner {
			return 1, true
		}
		return posixLinkOwner(path, info)
	}
	err := checkBoxAncestorsWith(filepath.Join(outer, "box.json"), os.Lstat, os.Readlink, owner)
	require.Error(t, err, "a user-owned link reached through a root-owned alias must be refused")
	require.Contains(t, err.Error(), inner)
	require.Contains(t, err.Error(), filepath.Join(target, "box.json"))
}
