package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/safepath"
)

// The coverage card for diskguard.go (cover-cmd-nova-swarm-diskguard.w1): the helpers a
// guard's own run is built from and that no test reached before. cleanModCache, guardRoots,
// unique, loginGoCaches and heldPaths have no seam of their own, so each is driven directly:
// a fixture under t.TempDir(), the login's caches read from the environment as the login
// sets it, and the open paths read from this machine's own /proc on Linux. landDirty and
// processList are not here: each reaches a real git or ps child, a subprocess the unit tier
// refuses, and neither has a seam to fake; the report says so. Nothing here sleeps, reads
// the clock, or touches a store.

// cleanModCache empties a module cache as go clean -modcache does: every entry goes, a
// read-only one included, and the directory itself stays. A directory it cannot read is an
// error, and an entry that is a symlink is refused by safepath rather than followed.
func TestDiskguardCoverCleanModCache(t *testing.T) {
	t.Parallel()

	t.Run("every entry goes and the directory stays", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		readonly := filepath.Join(dir, "a@v1")
		require.NoError(t, os.MkdirAll(readonly, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(readonly, "a.go"), []byte("package a\n"), 0o644))
		require.NoError(t, os.Chmod(readonly, 0o500))
		writable := filepath.Join(dir, "b@v2")
		require.NoError(t, os.MkdirAll(writable, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(writable, "b.go"), []byte("package b\n"), 0o644))

		require.NoError(t, cleanModCache(dir))

		assert.DirExists(t, dir, "native made the directory for its children; it stays")
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})

	t.Run("a directory it cannot read is an error", func(t *testing.T) {
		t.Parallel()
		err := cleanModCache(filepath.Join(t.TempDir(), "absent"))
		require.Error(t, err)
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})

	t.Run("an entry that is a symlink is refused, not followed", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		target := filepath.Join(t.TempDir(), "keep")
		require.NoError(t, os.WriteFile(target, []byte("keep\n"), 0o644))
		require.NoError(t, os.Symlink(target, filepath.Join(dir, "link")))

		err := cleanModCache(dir)
		var refused *safepath.Refused
		require.ErrorAs(t, err, &refused)
		assert.Contains(t, refused.Reason, "symlink")
		assert.FileExists(t, target, "the link's target is never removed")
	})
}

// guardRoots is every --root and every subdirectory of a --scan holding slots/, once: a
// root is taken as given, a scan that cannot be read or whose child holds no slots/ is
// skipped, and the result is unique. The tilde seam is applied to both.
func TestDiskguardCoverGuardRoots(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	scan := filepath.Join(base, "scan")
	pool := filepath.Join(scan, "pool-a")
	require.NoError(t, os.MkdirAll(filepath.Join(pool, "slots"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(scan, "no-slots"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(scan, "a-file"), []byte("x"), 0o644))
	direct := filepath.Join(base, "direct")

	t.Run("a root is taken as given and a scan adds each slot pool once", func(t *testing.T) {
		t.Parallel()
		got := guardRoots([]string{direct, direct, ""}, []string{scan, filepath.Join(base, "absent")}, func(p string) string { return p })
		assert.Equal(t, []string{direct, pool}, got,
			"the duplicate and the empty root go; the unreadable scan, the child without slots/ and the plain file are skipped")
	})

	t.Run("tilde expands every root and scan", func(t *testing.T) {
		t.Parallel()
		tilde := func(p string) string {
			if p == "~scan" {
				return scan
			}
			return filepath.Join(base, p)
		}
		got := guardRoots([]string{"direct"}, []string{"~scan"}, tilde)
		assert.Equal(t, []string{direct, pool}, got)
	})
}

// unique is a list of paths with each once, in order, the empty one dropped.
func TestDiskguardCoverUnique(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"order kept, duplicates and the empty path dropped", []string{"/b", "/a", "/b", "", "/c", ""}, []string{"/b", "/a", "/c"}},
		{"an empty list stays empty", nil, nil},
		{"only empty paths is no paths", []string{"", ""}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, unique(tc.in))
		})
	}
}

// loginGoCaches names the login's own Go build and module caches: GOCACHE/GOMODCACHE when
// the environment sets them, else go-build under the user cache and pkg/mod under the
// first GOPATH or ~/go.
func TestDiskguardCoverLoginGoCaches(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	build, mod := loginGoCaches(home)

	t.Run("the build cache is GOCACHE when set, else the user cache's go-build", func(t *testing.T) {
		t.Parallel()
		if got := os.Getenv("GOCACHE"); got != "" {
			assert.Equal(t, got, build)
			return
		}
		cache, err := os.UserCacheDir()
		if err != nil {
			assert.Empty(t, build)
			return
		}
		assert.Equal(t, filepath.Join(cache, "go-build"), build)
	})

	t.Run("the module cache is GOMODCACHE when set, else pkg/mod under the first GOPATH or home", func(t *testing.T) {
		t.Parallel()
		if got := os.Getenv("GOMODCACHE"); got != "" {
			assert.Equal(t, got, mod)
			return
		}
		want := filepath.Join(home, "go", "pkg", "mod")
		if p := filepath.SplitList(os.Getenv("GOPATH")); len(p) > 0 && p[0] != "" {
			want = filepath.Join(p[0], "pkg", "mod")
		}
		assert.Equal(t, want, mod)
	})

	t.Run("a build cache is always named", func(t *testing.T) {
		t.Parallel()
		assert.NotEmpty(t, build)
	})
}

// heldPaths reads every path a process other than this one holds. On Linux it reads /proc,
// which needs no child; on darwin it runs lsof, a subprocess the unit tier refuses, so the
// test covers Linux and says so elsewhere. Every path the kernel reports is absolute and
// listed once. The refusal (an unreadable /proc) is not reachable on a live machine.
func TestDiskguardCoverHeldPaths(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("heldPaths runs lsof on this platform, a subprocess the unit tier refuses")
	}
	paths, err := heldPaths()
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, p := range paths {
		assert.True(t, filepath.IsAbs(p), "the kernel reports an absolute path, got %q", p)
		assert.False(t, seen[p], "a path is listed once, got %q twice", p)
		seen[p] = true
	}
}
