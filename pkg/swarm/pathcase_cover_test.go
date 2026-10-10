package swarm

// pathcase_cover_test.go is the unit cover for AbsResolved (pathcase.go:159),
// the one function in this file the unit tier's per-function table showed at
// 0.0%. AbsResolved makes a path absolute and then symlink-resolves it, so the
// spelling admission a run records for a slot or a root agrees with the name the
// filesystem itself uses -- every later compare against it is then sound.
//
// These tests hold what is reachable without any process: each plants a
// throwaway filesystem state in t.TempDir(), no real clock, no network, no
// subprocess, no store. The EXPECTED side of every compare is resolved at
// setup: on darwin t.TempDir() is handed out under /var, which IS a symlink
// to /private/var, and AbsResolved deliberately answers the resolved spelling
// (pathcase.go:149-153) -- so both sides carry the one name the filesystem
// uses before they are compared, as the package's own tests already do
// (slot_containment_test.go:21-28, toolchain_test.go:44-48).
// They cover the resolvable branches:
//   - an existing path resolves whole and is cleaned (the main path, pathcase.go:168);
//   - a symlink is followed so the answer is the target's spelling, not the link's;
//   - a path whose ancestor exists but whose tail does not is answered from the
//     resolved ancestor joined with the unresolved tail -- the climb at pathcase.go:176/177
//     landing on the early return at pathcase.go:170;
//   - a symlinked ancestor is resolved by the climb before the tail is joined on.
//
// Two branches AbsResolved cannot reach in a parallel, store-free, subprocess-free
// unit test are deliberately NOT claimed here, in the same spirit as
// opencode_cover_test.go ("held what is reachable without any process"):
//   - the `filepath.Abs` refusal (pathcase.go:162) needs os.Getwd to fail, which
//     only happens when the process working directory has been deleted, and
//     TestEveryTestOpensWithTParallel (internal/ci) forbids a t.Chdir/os.Chdir that
//     would mutate the whole process -- there is no seam to inject a fake cwd;
//   - the `parent == cur` return (pathcase.go:174) needs the filesystem root itself
//     to be unresolvable, which never happens on a mounted Unix volume.
// Each is a job for the functional tier should a seam or a process ever exist; the
// resolvable cases pin the spelling admission this card is about.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolvedTempDir is t.TempDir() in the spelling the filesystem itself uses,
// so the expected side of an AbsResolved compare carries the one name the
// answer carries too: on darwin the temp dir sits under /var, a symlink to
// /private/var, and AbsResolved returns the resolved one (pathcase.go:149-153).
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return dir
}

// TestPathcaseCoverAbsResolvedResolvesAnExistingPath pins the main path,
// pathcase.go:168: a path that exists resolves whole and is cleaned, so a
// redundant slash or a `.` segment does not leak into the recorded spelling.
func TestPathcaseCoverAbsResolvedResolvesAnExistingPath(t *testing.T) {
	t.Parallel()

	root := resolvedTempDir(t)
	file := filepath.Join(root, "slot")
	require.NoError(t, os.WriteFile(file, []byte("k\n"), 0o644))

	for _, tc := range []struct {
		name string
		path string
	}{
		{"an absolute path to an existing file resolves to itself", file},
		{"a trailing slash is cleaned away", file + "/"},
		{"a dot segment in the tail is cleaned away", filepath.Join(file, ".")},
		{"a dot-dot segment that stays in place is cleaned away", filepath.Join(root, "slot", "..", "slot")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := AbsResolved(tc.path)
			require.NoError(t, err, "an existing path is resolved, not refused: %v", err)
			assert.Equal(t, file, got, "the recorded spelling is the cleaned, real path, not the input spelling")
		})
	}
}

// TestPathcaseCoverAbsResolvedFollowsASymlink pins the case the doc comment
// calls out (pathcase.go:145): a symlink is refused as the answer -- the
// target's spelling is the one admission records -- so two spellings that differ
// only in case land on the right inode.
func TestPathcaseCoverAbsResolvedFollowsASymlink(t *testing.T) {
	t.Parallel()

	t.Run("a symlink resolves to its target, not its own spelling", func(t *testing.T) {
		t.Parallel()
		root := resolvedTempDir(t)
		target := filepath.Join(root, "worker-1")
		require.NoError(t, os.WriteFile(target, []byte("k\n"), 0o644))
		link := filepath.Join(root, "worker-2")
		require.NoError(t, os.Symlink(target, link), "setup: the link must point at the existing target")

		got, err := AbsResolved(link)
		require.NoError(t, err)
		assert.Equal(t, target, got, "the link's spelling is the refused one; the target's is the answer")
	})

	// A symlink to a directory, asked for whole, resolves to the directory.
	t.Run("a symlink to a directory resolves to the directory", func(t *testing.T) {
		t.Parallel()
		root := resolvedTempDir(t)
		target := filepath.Join(root, "worker-1")
		require.NoError(t, os.MkdirAll(target, 0o755))
		link := filepath.Join(root, "worker-2")
		require.NoError(t, os.Symlink(target, link), "setup: the link must point at the existing directory")

		got, err := AbsResolved(link)
		require.NoError(t, err)
		assert.Equal(t, target, got)
	})
}

// TestPathcaseCoverAbsResolvedResolvesAnExistingAncestor pins the climb
// (pathcase.go:176/177) and its early return at pathcase.go:170: a path that does
// not exist yet is answered from the deepest existing ancestor, with the
// unresolved tail joined on, never guessed from runtime.GOOS.
func TestPathcaseCoverAbsResolvedResolvesAnExistingAncestor(t *testing.T) {
	t.Parallel()

	root := resolvedTempDir(t)

	for _, tc := range []struct {
		name string
		tail string
	}{
		{"one unresolved level below an existing root", filepath.Join("notyet")},
		{"two unresolved levels below an existing root", filepath.Join("notyet", "child")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := filepath.Join(root, tc.tail)
			got, err := AbsResolved(want)
			require.NoError(t, err, "an absent tail is resolved from its ancestor, not refused: %v", err)
			assert.Equal(t, want, got, "the deepest existing ancestor is resolved and the unresolved tail is joined onto it")
		})
	}
}

// TestPathcaseCoverAbsResolvedResolvesASymlinkedAncestor pins the case the
// pathcase.go doc comment measures for (issue #159): the climb must resolve a
// symlinked ancestor before joining the tail, so a slot spelled through a case
// fold at a mount boundary is matched against the volume's real name.
func TestPathcaseCoverAbsResolvedResolvesASymlinkedAncestor(t *testing.T) {
	t.Parallel()

	t.Run("a symlinked ancestor is resolved before the unresolved tail is joined", func(t *testing.T) {
		t.Parallel()
		root := resolvedTempDir(t)
		real := filepath.Join(root, "worker-1")
		require.NoError(t, os.MkdirAll(real, 0o755))
		link := filepath.Join(root, "worker-2")
		require.NoError(t, os.Symlink(real, link), "setup: the link must point at the existing ancestor directory")

		got, err := AbsResolved(filepath.Join(link, "notyet", "child"))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(real, "notyet", "child"), got,
			"the climb resolves the symlinked ancestor first, then joins the unresolved tail onto the target")
	})
}
