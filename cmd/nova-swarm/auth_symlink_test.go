package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const authSymlinkStay = "stay-put\n"

func plantAuthSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this filesystem will not make a symlink: %v", err)
	}
}

func authSymlinkSource(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(p, []byte(`{"fake":"marker"}`), 0o600))
	return p
}

func authSymlinkNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// copyAuth writes the provider body. A symlink at auth.json, or a directory
// symlink at opencode, must not receive it.
func TestCopyAuthDoesNotFollowASymlink(t *testing.T) {
	t.Parallel()

	t.Run("file", func(t *testing.T) {
		t.Parallel()
		dataHome := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside")
		require.NoError(t, os.WriteFile(outside, []byte(authSymlinkStay), 0o644))
		link := filepath.Join(dataHome, "auth.json")
		plantAuthSymlink(t, outside, link)
		reason := copyAuth(authSymlinkSource(t), "fake", dataHome)
		require.NotEmpty(t, reason)
		assert.Contains(t, reason, "symlink")
		raw, err := os.ReadFile(outside)
		require.NoError(t, err)
		assert.Equal(t, authSymlinkStay, string(raw), "copyAuth followed the symlink")
		fi, err := os.Lstat(link)
		require.NoError(t, err)
		assert.NotZero(t, fi.Mode()&os.ModeSymlink, "the planted link was replaced")
	})
	t.Run("directory", func(t *testing.T) {
		t.Parallel()
		dataHome := t.TempDir()
		outside := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(outside, "kept"), []byte(authSymlinkStay), 0o644))
		plantAuthSymlink(t, outside, filepath.Join(dataHome, "opencode"))
		before := authSymlinkNames(t, outside)
		reason := copyAuth(authSymlinkSource(t), "fake", dataHome)
		require.NotEmpty(t, reason)
		assert.Contains(t, reason, "symlink")
		assert.Equal(t, before, authSymlinkNames(t, outside), "copyAuth followed the directory symlink")
		fi, err := os.Lstat(filepath.Join(dataHome, "opencode"))
		require.NoError(t, err)
		assert.NotZero(t, fi.Mode()&os.ModeSymlink, "the planted link was replaced")
	})
}

// writeJobConfig writes opencode.json under the data home. A symlink at .config,
// or at the file itself, must not receive it.
func TestWriteJobConfigDoesNotFollowASymlink(t *testing.T) {
	t.Parallel()

	t.Run("ancestor", func(t *testing.T) {
		t.Parallel()
		dataHome := t.TempDir()
		outside := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(outside, "kept"), []byte(authSymlinkStay), 0o644))
		plantAuthSymlink(t, outside, filepath.Join(dataHome, ".config"))
		before := authSymlinkNames(t, outside)
		var notes bytes.Buffer
		sha, reason, proxy := writeJobConfig(nativeRunConfig{}, "fake", dataHome, t.TempDir(), nil, &notes)
		if proxy != nil {
			require.NoError(t, proxy.Close())
		}
		require.NotEmpty(t, reason)
		assert.Contains(t, reason, "symlink")
		assert.Empty(t, sha)
		assert.Equal(t, before, authSymlinkNames(t, outside), "writeJobConfig followed the directory symlink")
		fi, err := os.Lstat(filepath.Join(dataHome, ".config"))
		require.NoError(t, err)
		assert.NotZero(t, fi.Mode()&os.ModeSymlink, "the planted link was replaced")
	})
	t.Run("file", func(t *testing.T) {
		t.Parallel()
		dataHome := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dataHome, ".config", "opencode"), 0o755))
		outside := filepath.Join(t.TempDir(), "outside")
		require.NoError(t, os.WriteFile(outside, []byte(authSymlinkStay), 0o644))
		link := filepath.Join(dataHome, ".config", "opencode", "opencode.json")
		plantAuthSymlink(t, outside, link)
		var notes bytes.Buffer
		sha, reason, proxy := writeJobConfig(nativeRunConfig{}, "fake", dataHome, t.TempDir(), nil, &notes)
		if proxy != nil {
			require.NoError(t, proxy.Close())
		}
		require.NotEmpty(t, reason)
		assert.Contains(t, reason, "symlink")
		assert.Empty(t, sha)
		raw, err := os.ReadFile(outside)
		require.NoError(t, err)
		assert.Equal(t, authSymlinkStay, string(raw), "writeJobConfig followed the symlink")
		fi, err := os.Lstat(link)
		require.NoError(t, err)
		assert.NotZero(t, fi.Mode()&os.ModeSymlink, "the planted link was replaced")
	})
}

// removeAuthCopy unlinks a planted symlink and does not delete the file it names.
func TestRemoveAuthCopyDoesNotFollowASymlink(t *testing.T) {
	t.Parallel()

	t.Run("file", func(t *testing.T) {
		t.Parallel()
		dataHome := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside")
		require.NoError(t, os.WriteFile(outside, []byte(authSymlinkStay), 0o600))
		link := filepath.Join(dataHome, "auth.json")
		plantAuthSymlink(t, outside, link)
		var errOut bytes.Buffer
		left := removeAuthCopy(dataHome, &errOut)
		require.Empty(t, left, "a file symlink was left in place: %v\n%s", left, errOut.String())
		raw, err := os.ReadFile(outside)
		require.NoError(t, err)
		assert.Equal(t, authSymlinkStay, string(raw), "removeAuthCopy followed the symlink and removed the target")
		_, err = os.Lstat(link)
		assert.ErrorIs(t, err, os.ErrNotExist, "the symlink itself was not unlinked")
	})
	t.Run("directory", func(t *testing.T) {
		t.Parallel()
		dataHome := t.TempDir()
		outside := t.TempDir()
		marker := filepath.Join(outside, "auth.json")
		require.NoError(t, os.WriteFile(marker, []byte(authSymlinkStay), 0o600))
		link := filepath.Join(dataHome, "opencode")
		plantAuthSymlink(t, outside, link)
		require.NoError(t, os.WriteFile(filepath.Join(dataHome, "auth.json"), []byte("{}"), 0o600))
		var errOut bytes.Buffer
		left := removeAuthCopy(dataHome, &errOut)
		require.Empty(t, left, "a directory symlink was left in place: %v\n%s", left, errOut.String())
		raw, err := os.ReadFile(marker)
		require.NoError(t, err)
		assert.Equal(t, authSymlinkStay, string(raw), "removeAuthCopy followed the directory symlink and removed the target")
		_, err = os.Lstat(link)
		assert.ErrorIs(t, err, os.ErrNotExist, "the directory symlink itself was not unlinked")
		_, err = os.Lstat(filepath.Join(dataHome, "auth.json"))
		assert.ErrorIs(t, err, os.ErrNotExist, "the real auth copy in the data home was left behind")
	})
}
