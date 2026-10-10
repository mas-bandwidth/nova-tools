package secrets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckInvariant5DotDotNamedDirIsInsideTheStore: a key file under a
// directory named "..cache" inside the store is refused, while a key outside
// the store (reached by "../x") is not reported as inside it.
func TestCheckInvariant5DotDotNamedDirIsInsideTheStore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := filepath.Join(root, "store")
	require.NoError(t, os.MkdirAll(store, 0o755))
	inside := func(keyPath string) bool {
		for _, f := range CheckInvariant5(store, keyPath) {
			if f.Kind == "store-private-key" && f.Reason == "key file is inside store directory" {
				return true
			}
		}
		return false
	}
	assert.True(t, inside(filepath.Join(store, "..cache", "key.txt")), "a key under store/..cache was not reported as inside the store")
	assert.True(t, inside(filepath.Join(store, "sub", "key.txt")), "a key under store/sub was not reported as inside the store")
	assert.False(t, inside(filepath.Join(store, "..", "x")), "store/../x was reported as inside the store")
	assert.False(t, inside(filepath.Join(root, "key.txt")), "a key beside the store was reported as inside it")
}

// TestCheckInvariant5FollowsSymlinks: a key is inside the store by where it lands. A key
// file that is a link into the store, a key under a directory that is a link into the
// store, and a key spelled through a link to the store itself are all inside it; a key
// that only has a link to somewhere else in its path is not.
func TestCheckInvariant5FollowsSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := filepath.Join(root, "store")
	other := filepath.Join(root, "other")
	for _, d := range []string{filepath.Join(store, "sub"), other} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	link := func(target, name string) string {
		p := filepath.Join(root, name)
		if err := os.Symlink(target, p); err != nil {
			t.Skipf("symlinks are not available here: %v", err)
		}
		return p
	}
	inside := func(keyPath, storeDir string) bool {
		for _, f := range CheckInvariant5(storeDir, keyPath) {
			if f.Kind == "store-private-key" && f.Reason == "key file is inside store directory" {
				return true
			}
		}
		return false
	}
	file := filepath.Join(store, "sub", "key.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	fileLink := link(file, "key-link")
	dirLink := link(filepath.Join(store, "sub"), "sub-link")
	storeLink := link(store, "store-link")
	otherLink := link(other, "other-link")

	assert.True(t, inside(fileLink, store), "a key file that is a link into the store was not inside it")
	assert.True(t, inside(filepath.Join(dirLink, "absent.txt"), store), "a key under a directory that is a link into the store was not inside it")
	assert.True(t, inside(file, storeLink), "a key in the store was not inside it when the store was named through a link")
	assert.False(t, inside(filepath.Join(otherLink, "key.txt"), store), "a key under a link to somewhere else was reported inside the store")
	assert.False(t, inside(filepath.Join(other, "key.txt"), store), "a key beside the store was reported inside it")
}
