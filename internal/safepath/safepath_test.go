package safepath

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(p, 0o755))
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// name-ok-is-one-path-element: only [A-Za-z0-9._-]+, never empty, never ".",
// never "..", never a slash and never a leading dash.
func TestNameOKIsOnePathElement(t *testing.T) {
	t.Parallel()

	ok := []string{"3", "card-9322", "a.b_c-d", "..hidden", "x..y"}
	for _, s := range ok {
		assert.True(t, NameOK(s), "NameOK(%q) = false, want true", s)
	}
	bad := []string{"", ".", "..", "../escape", "a/b", "-flag", "two words", "tab\t"}
	for _, s := range bad {
		assert.False(t, NameOK(s), "NameOK(%q) = true, want false", s)
	}
}

// remove-under-is-the-only-rm: a directory strictly below a root goes.
func TestRemoveUnderRootsRemovesBelowRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	victim := filepath.Join(root, "slot", "jobs", "card-1")
	mustWrite(t, filepath.Join(victim, "scratch", "s"), "x")
	err := RemoveUnderRoots(victim, root)
	require.NoError(t, err, "RemoveUnderRoots(%q, %q) = %v, want nil", victim, root, err)
	require.False(t, exists(victim), "RemoveUnder left %s behind", victim)
}

// a path outside every root is refused, and left where it is.
func TestRemoveUnderRefusesOutsideTheRoots(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "keep")
	mustMkdir(t, victim)
	require.Error(t, RemoveUnderRoots(victim, root), "RemoveUnderRoots(%q, %q) = nil, want a refusal", victim, root)
	require.True(t, exists(victim), "RemoveUnder removed %s, a path outside the root", victim)
}

// a path containing ".." is refused even when it would resolve below the root.
func TestRemoveUnderRefusesDotDot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "slot")
	mustMkdir(t, target)
	escape := root + string(os.PathSeparator) + "slot" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "slot"
	require.Error(t, RemoveUnderRoots(escape, root), "RemoveUnderRoots(%q, %q) = nil, want a refusal for \"..\"", escape, root)
	require.True(t, exists(target), "RemoveUnder removed %s through a \"..\" element", target)
}

// a path that IS a symlink is refused; the target survives.
func TestRemoveUnderRefusesSymlinkPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "keep"), "x")
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(outside, link))
	require.Error(t, RemoveUnderRoots(link, root), "RemoveUnderRoots(%q, %q) = nil, want a refusal for a symlink", link, root)
	require.True(t, exists(filepath.Join(outside, "keep")), "RemoveUnder followed the symlink and removed its target")
}

// a symlink on an intermediate component that points outside the root is
// refused too: the resolved path is not below the root.
func TestResolvedUnderRefusesSymlinkEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "keep"), "x")
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "gate")))
	escape := filepath.Join(root, "gate", "keep")
	_, err := ResolvedUnder(escape, root)
	require.Error(t, err, "ResolvedUnder(%q, %q) = nil, want a refusal for a symlink escape", escape, root)
	require.True(t, exists(filepath.Join(outside, "keep")), "ResolvedUnder accepted a path that resolves outside the root")
}
