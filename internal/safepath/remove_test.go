package safepath

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// safepath's contract, in the order the danger arrives: an empty path, the root itself, a
// path that resolves outside the root, a path that IS a symlink, and a root that is the
// whole disk or the user's home. Every one of them is a refusal, and every refusal leaves
// the bytes where they were. Glenn, 2026-09-17: "It is just one mistake away from deleting
// the whole disk."
func TestRemoveUnderRefusesUnsafePaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	inside := filepath.Join(root, "inside")
	require.NoError(t, os.MkdirAll(inside, 0o755))

	outside := t.TempDir()
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(outside, link))
	// A path that only LOOKS like it is under the root: the symlink in the middle of it
	// resolves to another tree, so EvalSymlinks on both must catch it.
	escaped := filepath.Join(link, "escaped")
	require.NoError(t, os.MkdirAll(escaped, 0o755))

	home, err := os.UserHomeDir()
	require.NoError(t, err, "this machine has no home directory to test the refusal against")

	cases := []struct {
		name string
		root string
		path string
	}{
		{"empty path", root, ""},
		{"empty root", "", inside},
		{"path is the root", root, root},
		{"path is not below the root", root, outside},
		{"path escapes the root through a symlink", root, escaped},
		{"path is itself a symlink", root, link},
		{"root is the whole disk", "/", filepath.Join(root, "inside")},
		{"root is the user's home", home, filepath.Join(home, "somewhere")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Error(t, RemoveUnder(c.root, c.path), "RemoveUnder(%q, %q) returned nil; it must refuse", c.root, c.path)
			// Nothing under the temp root may have moved, and the symlink target must
			// still be there: a refusal is not a partial removal.
			for _, p := range []string{inside, link, outside} {
				_, err := os.Lstat(p)
				assert.NoError(t, err, "the refusal removed %s", p)
			}
		})
	}
}

// The one path it removes: a plain directory strictly below a root the caller named.
func TestRemoveUnderRemovesBelowRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	require.NoError(t, os.MkdirAll(filepath.Join(sub, "deep", "deeper"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "file"), []byte("x"), 0o644))
	require.NoError(t, RemoveUnder(root, sub), "RemoveUnder removed nothing")
	_, err := os.Lstat(sub)
	require.True(t, os.IsNotExist(err), "the directory below the root is still there: %v", err)
	_, err = os.Lstat(root)
	require.NoError(t, err, "the root itself was removed")
}
