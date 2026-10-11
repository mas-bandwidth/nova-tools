package swarm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPrewarmCoverLispCacheDir pins LispCacheDir's join of root, CacheDirName
// and "common-lisp". The function is a pure path join with no refusal path;
// each row verifies a different root shape.
func TestPrewarmCoverLispCacheDir(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		root string
		want string
	}{
		{"absolute root", "/srv/swarm", filepath.Join("/srv/swarm", CacheDirName, "common-lisp")},
		{"relative root", "swarm", filepath.Join("swarm", CacheDirName, "common-lisp")},
		{"empty root", "", filepath.Join(CacheDirName, "common-lisp")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, LispCacheDir(tc.root))
		})
	}
}

// TestPrewarmCoverAsdfOutputTranslations pins asdfOutputTranslations building
// the ASDF output-translations directive from cache and source paths. The main
// path resolves symlinks when both paths exist; the refusal path uses raw paths
// when EvalSymlinks fails on non-existent directories.
func TestPrewarmCoverAsdfOutputTranslations(t *testing.T) {
	t.Parallel()

	t.Run("main resolves existing paths", func(t *testing.T) {
		t.Parallel()

		base := t.TempDir()
		cachePath := filepath.Join(base, "cache")
		sourceRoot := filepath.Join(base, "src")
		require.NoError(t, os.MkdirAll(cachePath, 0o755))
		require.NoError(t, os.MkdirAll(sourceRoot, 0o755))

		got := asdfOutputTranslations(cachePath, sourceRoot)
		assert.Contains(t, got, "(:output-translations")
		assert.Contains(t, got, ":implementation")
		assert.Contains(t, got, ":ignore-inherited-configuration")

		resolvedCache, err := filepath.EvalSymlinks(cachePath)
		require.NoError(t, err)
		assert.Contains(t, got, filepath.ToSlash(resolvedCache))
		resolvedSrc, err := filepath.EvalSymlinks(sourceRoot)
		require.NoError(t, err)
		assert.Contains(t, got, filepath.ToSlash(resolvedSrc))
	})

	t.Run("refusal non-existent paths", func(t *testing.T) {
		t.Parallel()

		got := asdfOutputTranslations("/no/such/cache", "/no/such/src")
		assert.Contains(t, got, "(:output-translations")
		assert.Contains(t, got, ":implementation")
		assert.Contains(t, got, ":ignore-inherited-configuration")
		assert.Contains(t, got, "/no/such/cache")
		assert.Contains(t, got, "/no/such/src")
	})
}

// TestPrewarmCoverJobASDFOutputTranslations pins JobASDFOutputTranslations
// deriving the cache path from the source root's job directory via
// JobLispCacheDir and delegating to asdfOutputTranslations.
func TestPrewarmCoverJobASDFOutputTranslations(t *testing.T) {
	t.Parallel()

	t.Run("main derives cache from source root", func(t *testing.T) {
		t.Parallel()

		base := t.TempDir()
		sourceRoot := filepath.Join(base, "job", "repo")
		require.NoError(t, os.MkdirAll(sourceRoot, 0o755))

		cacheDir := JobLispCacheDir(sourceRoot)
		require.NoError(t, os.MkdirAll(cacheDir, 0o755))

		got := JobASDFOutputTranslations(sourceRoot)
		assert.Contains(t, got, "(:output-translations")
		assert.Contains(t, got, ":implementation")
		assert.Contains(t, got, ":ignore-inherited-configuration")

		resolvedCache, err := filepath.EvalSymlinks(cacheDir)
		require.NoError(t, err)
		assert.Contains(t, got, filepath.ToSlash(resolvedCache))
		resolvedSrc, err := filepath.EvalSymlinks(sourceRoot)
		require.NoError(t, err)
		assert.Contains(t, got, filepath.ToSlash(resolvedSrc))
	})

	t.Run("refusal non-existent source root", func(t *testing.T) {
		t.Parallel()

		got := JobASDFOutputTranslations("/no/such/repo")
		assert.Contains(t, got, "(:output-translations")
		assert.Contains(t, got, ":implementation")
		assert.Contains(t, got, ":ignore-inherited-configuration")
		assert.Contains(t, got, "/no/such/repo")
	})
}

// TestPrewarmCoverCopyLispSeed pins copyLispSeed copying a directory tree
// verbatim into the destination, and refusing when the seed does not exist.
func TestPrewarmCoverCopyLispSeed(t *testing.T) {
	t.Parallel()

	t.Run("main copies tree", func(t *testing.T) {
		t.Parallel()

		seed := t.TempDir()
		dest := filepath.Join(t.TempDir(), "dest")
		require.NoError(t, os.MkdirAll(dest, 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(seed, "sub"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(seed, "file.txt"), []byte("hello"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(seed, "sub", "nested.txt"), []byte("world"), 0o644))

		require.NoError(t, copyLispSeed(seed, dest))

		data, err := os.ReadFile(filepath.Join(dest, "file.txt"))
		require.NoError(t, err)
		assert.Equal(t, "hello", string(data))
		data, err = os.ReadFile(filepath.Join(dest, "sub", "nested.txt"))
		require.NoError(t, err)
		assert.Equal(t, "world", string(data))
	})

	t.Run("refusal non-existent seed", func(t *testing.T) {
		t.Parallel()

		seed := filepath.Join(t.TempDir(), "missing")
		dest := filepath.Join(t.TempDir(), "dest")
		err := copyLispSeed(seed, dest)
		require.Error(t, err)
	})
}
