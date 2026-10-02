package testkit_test

import (
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

func TestTreeOverwritesAFileAlreadyThere(t *testing.T) {
	t.Parallel()
	root := testkit.Tree(t, filepath.Join(t.TempDir(), "made"), map[string]string{"go.mod": "module x\n"})
	testkit.Tree(t, root, map[string]string{"go.mod": "module y\n", "sub/new.txt": "beside it"})
	assert.Equal(t, "module y\n", testkit.ReadFile(t, filepath.Join(root, "go.mod")))
	assert.Equal(t, "beside it", testkit.ReadFile(t, filepath.Join(root, "sub", "new.txt")))
}

// A name that is lexically inside the root but reaches a sibling directory
// through a symlink already in the root is refused, and the sibling is left
// empty: the writes go through an os.Root.
func TestTreeRefusesToWriteThroughASymlinkOutOfTheRoot(t *testing.T) {
	t.Parallel()
	testkit.SkipOn(t, "windows", "creating a symlink needs a privilege a test does not have")
	root, sibling := t.TempDir(), t.TempDir()
	require.NoError(t, os.Symlink(sibling, filepath.Join(root, "link")))
	for _, name := range []string{"link/file.txt", "link/deeper/file.txt"} {
		rec := &recorder{TB: t}
		runs(rec, func() { testkit.Tree(rec, root, map[string]string{name: "outside"}) })
		assert.True(t, rec.failed, "Tree wrote %q through the link", name)
		assert.Contains(t, rec.msg, `tree: "`+name+`" under `+root)
	}
	entries, err := os.ReadDir(sibling)
	require.NoError(t, err)
	assert.Empty(t, entries, "a write through the link reached the sibling directory")
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
