package swarm

// reap_cover_test.go reaches the five reap.go functions the unit tier's
// per-function coverage table showed at 0.0%: CacheRoot (reap.go:21),
// GoModCacheDir (reap.go:25), GoBuildCacheDir (reap.go:29), NPMCacheDir
// (reap.go:32) and EnsureCacheDirs (reap.go:36). The four path functions are
// pure joins with no refusal branch of their own, so each row pins the exact
// name under the swarm root and the empty-root row pins the relative answer
// EnsureCacheDirs refuses to act on. EnsureCacheDirs is covered in full: main
// path, the blank-root no-op and the write error. Nothing here sleeps, reads
// a real clock, opens a socket, starts a process or touches a store; every
// write lands inside t.TempDir(). Every test is named TestReapCoverSomething
// so `-run TestReapCover` selects them.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReapCoverCacheDirNames pins the shared cache directory names native
// hands every child: the one cache dir under a swarm root, and the go-mod,
// go-build and npm dirs inside it, each a child of CacheRoot, each relative
// when the root is empty.
func TestReapCoverCacheDirNames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		get  func(string) string
		want []string
	}{
		{"CacheRoot is the one shared cache dir", CacheRoot, []string{CacheDirName}},
		{"GoModCacheDir is the child's GOMODCACHE", GoModCacheDir, []string{CacheDirName, "go-mod"}},
		{"GoBuildCacheDir is the child's GOCACHE", GoBuildCacheDir, []string{CacheDirName, "go-build"}},
		{"NPMCacheDir is the child's NPM_CONFIG_CACHE", NPMCacheDir, []string{CacheDirName, "npm"}},
	} {
		t.Run(tc.name+" under a swarm root", func(t *testing.T) {
			t.Parallel()
			root := filepath.Join("swarm", "root-a")
			want := append([]string{root}, tc.want...)
			assert.Equal(t, filepath.Join(want...), tc.get(root),
				"%s: the dir under the swarm root is the one name the child is handed", tc.name)
			assert.True(t, strings.HasPrefix(tc.get(root), CacheRoot(root)),
				"%s: the child's cache dir must nest under the shared cache root", tc.name)
		})
		t.Run(tc.name+" with an empty root stays relative", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, filepath.Join(tc.want...), tc.get(""),
				"%s: an empty root answers a relative dir, which only EnsureCacheDirs refuses", tc.name)
		})
	}
}

// TestReapCoverEnsureCacheDirsMakesTheSharedDirs pins the main path: the four
// shared cache dirs exist under the root so the child's first cache write
// lands inside the write set, and a second call on dirs that exist is a no-op.
func TestReapCoverEnsureCacheDirsMakesTheSharedDirs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, EnsureCacheDirs(root), "making the shared cache dirs under the root failed")
	for _, dir := range []string{GoModCacheDir(root), GoBuildCacheDir(root), NPMCacheDir(root), LispCacheDir(root)} {
		info, err := os.Stat(dir)
		require.NoError(t, err, "%s must exist before the child's first cache write", dir)
		assert.True(t, info.IsDir(), "%s must be a directory", dir)
	}
	require.NoError(t, EnsureCacheDirs(root), "a second call on dirs that already exist is a no-op")
}

// TestReapCoverEnsureCacheDirsRefusesABlankRoot pins the guard refusal: an
// empty or whitespace root returns nil and makes no cache dir anywhere, so a
// blank root never writes into the working directory.
func TestReapCoverEnsureCacheDirsRefusesABlankRoot(t *testing.T) {
	t.Parallel()

	for _, root := range []string{"", "   "} {
		require.NoError(t, EnsureCacheDirs(root), "a blank root is a no-op, not an error")
		assert.NoDirExists(t, filepath.Join(root, CacheDirName),
			"a blank root must make no cache dir in the working directory")
	}
}

// TestReapCoverEnsureCacheDirsRefusesAFileRoot pins the write refusal: a root
// that is a regular file cannot hold the shared cache dirs, the mkdir error
// comes back named, and no dir is made.
func TestReapCoverEnsureCacheDirsRefusesAFileRoot(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644), "setup: the root must be a regular file")
	err := EnsureCacheDirs(file)
	require.Error(t, err, "a root that is a regular file must not pass as a swarm root")
	assert.ErrorContains(t, err, filepath.Base(file), "the error names the path that failed")
	assert.NoDirExists(t, GoModCacheDir(file), "the refusal stops before any cache dir is made")
}
