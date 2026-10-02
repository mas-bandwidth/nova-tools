package testkit_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTreeLaysDownEveryFileUnderTheRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	assert.Equal(t, root, testkit.Tree(t, root, map[string]string{"a.txt": "one", "deep/er/b.txt": "two"}))
	assert.Equal(t, "one", testkit.ReadFile(t, filepath.Join(root, "a.txt")))
	assert.Equal(t, "two", testkit.ReadFile(t, filepath.Join(root, "deep", "er", "b.txt")))
}

func TestTreeRefusesANameThatEscapesTheRootAndWritesNothing(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"../out.txt", "a/../../out.txt", "/abs.txt", ""} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			root := filepath.Join(parent, "root")
			rec := &recorder{TB: t}
			runs(rec, func() { testkit.Tree(rec, root, map[string]string{"fine.txt": "x", name: "y"}) })
			assert.True(t, rec.failed, "Tree wrote %q", name)
			assert.Contains(t, rec.msg, "escapes the root")
			entries, err := os.ReadDir(parent)
			require.NoError(t, err)
			assert.Empty(t, entries, "a refused tree wrote files")
		})
	}
}

func TestCopyTreeCopiesFilesAndModesAndSkipsGitAndLinks(t *testing.T) {
	t.Parallel()
	src := testkit.Tree(t, t.TempDir(), map[string]string{"a.txt": "one", "sub/b.txt": "two", ".git/HEAD": "ref", "run.sh": "#!/bin/sh\n"})
	require.NoError(t, os.Chmod(filepath.Join(src, "run.sh"), 0o755))
	if runtime.GOOS != "windows" {
		require.NoError(t, os.Symlink(filepath.Join(src, "a.txt"), filepath.Join(src, "link")))
	}
	dst := filepath.Join(t.TempDir(), "copy")
	testkit.WriteFile(t, filepath.Join(dst, "a.txt"), "stale")
	testkit.CopyTree(t, src, dst)
	assert.Equal(t, "one", testkit.ReadFile(t, filepath.Join(dst, "a.txt")), "a file already there is overwritten")
	assert.Equal(t, "two", testkit.ReadFile(t, filepath.Join(dst, "sub", "b.txt")))
	assert.NoDirExists(t, filepath.Join(dst, ".git"))
	_, err := os.Lstat(filepath.Join(dst, "link"))
	assert.ErrorIs(t, err, fs.ErrNotExist, "a link was copied")
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dst, "run.sh"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	}
}

func TestCopyTreeFailsTheTestNamingAMissingSource(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "missing")
	rec := &recorder{TB: t}
	runs(rec, func() { testkit.CopyTree(rec, missing, t.TempDir()) })
	assert.True(t, rec.failed, "CopyTree passed a missing source")
	assert.Contains(t, rec.msg, "copy tree "+missing)
}

func TestJSONDecodesOrFailsWithTheText(t *testing.T) {
	t.Parallel()
	type row struct {
		Card string `json:"card"`
		N    int    `json:"n"`
	}
	assert.Equal(t, row{"c1", 2}, testkit.JSON[row](t, `{"card":"c1","n":2}`))
	rec := &recorder{TB: t}
	runs(rec, func() { testkit.JSON[row](rec, `{"card":`) })
	assert.True(t, rec.failed, "JSON passed text that does not decode")
	assert.Contains(t, rec.msg, `decode JSON "{\"card\":"`)
}

func TestSkipOnSkipsOnlyOnTheNamedSystem(t *testing.T) {
	t.Parallel()
	rec := &recorder{TB: t}
	runs(rec, func() { testkit.SkipOn(rec, runtime.GOOS, "the property is not observable here") })
	assert.True(t, rec.skipped, "SkipOn ran on the system it names")
	assert.Equal(t, "skipped on "+runtime.GOOS+": the property is not observable here", rec.msg)
	rec = &recorder{TB: t}
	runs(rec, func() { testkit.SkipOn(rec, "plan9-not-this-one", "never") })
	assert.False(t, rec.skipped, "SkipOn skipped on another system")
}
