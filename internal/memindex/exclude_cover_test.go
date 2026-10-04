package memindex

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// excludeCoverFS is the tree the exclude tests read: a visible file, a visible
// directory, the .git directory every tree hides, and a caller-excluded
// directory whose descendant is named directly.
func excludeCoverFS() fstest.MapFS {
	return fstest.MapFS{
		"a.txt":        {Data: []byte("alpha")},
		"keep/b.txt":   {Data: []byte("bravo")},
		".git/config":  {Data: []byte("[core]")},
		"secret/c.txt": {Data: []byte("charlie")},
	}
}

// TestExcludeCoverExcludingWrapsTree pins Excluding's main path: the returned
// fs.FS serves the visible tree unchanged, so a caller can read a file with no
// exclusion in its path.
func TestExcludeCoverExcludingWrapsTree(t *testing.T) {
	t.Parallel()

	fsys := Excluding(excludeCoverFS(), nil)
	data, err := fs.ReadFile(fsys, "a.txt")
	require.NoError(t, err)
	assert.Equal(t, "alpha", string(data))
}

// TestExcludeCoverHidden pins hidden's main path and its refusal: an ancestor
// named .git, or one the caller's predicate names, hides the whole subtree; a
// nil predicate and an unexcluded path do not.
func TestExcludeCoverHidden(t *testing.T) {
	t.Parallel()

	exclude := func(p string) bool { return p == "secret" }
	cases := []struct {
		name   string
		v      excludingFS
		target string
		want   bool
	}{
		{name: "plain path is visible", v: excludingFS{FS: excludeCoverFS(), exclude: exclude}, target: "a.txt", want: false},
		{name: "visible directory path is visible", v: excludingFS{FS: excludeCoverFS(), exclude: exclude}, target: "keep/b.txt", want: false},
		{name: "git file is hidden", v: excludingFS{FS: excludeCoverFS(), exclude: exclude}, target: ".git/config", want: true},
		{name: "excluded directory is hidden", v: excludingFS{FS: excludeCoverFS(), exclude: exclude}, target: "secret", want: true},
		{name: "excluded descendant is hidden by its ancestor", v: excludingFS{FS: excludeCoverFS(), exclude: exclude}, target: "secret/c.txt", want: true},
		{name: "nil predicate hides only git", v: excludingFS{FS: excludeCoverFS(), exclude: nil}, target: "secret/c.txt", want: false},
		{name: "nil predicate still hides git", v: excludingFS{FS: excludeCoverFS(), exclude: nil}, target: ".git/config", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.v.hidden(tc.target))
		})
	}
}

// TestExcludeCoverOpenServesVisibleAndRefusesHidden pins Open's main path and
// its refusal: a visible file reads, while a hidden path answers the same
// fs.ErrNotExist an absent file would.
func TestExcludeCoverOpenServesVisibleAndRefusesHidden(t *testing.T) {
	t.Parallel()

	fsys := Excluding(excludeCoverFS(), func(p string) bool { return p == "secret" })

	f, err := fsys.Open("keep/b.txt")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	cases := []struct {
		name string
		path string
	}{
		{name: "git path is refused", path: ".git/config"},
		{name: "excluded directory is refused", path: "secret"},
		{name: "excluded descendant is refused", path: "secret/c.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := fsys.Open(tc.path)
			require.Error(t, err)
			assert.ErrorIs(t, err, fs.ErrNotExist)
		})
	}
}

// TestExcludeCoverReadDirFiltersHiddenEntriesAndRefusesHiddenDir pins
// ReadDir's main path and its refusal: the listing drops .git and excluded
// children, and reading a hidden directory is refused as absent.
func TestExcludeCoverReadDirFiltersHiddenEntriesAndRefusesHiddenDir(t *testing.T) {
	t.Parallel()

	fsys := Excluding(excludeCoverFS(), func(p string) bool { return p == "secret" })

	entries, err := fs.ReadDir(fsys, ".")
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	assert.ElementsMatch(t, []string{"a.txt", "keep"}, names)

	_, err = fs.ReadDir(fsys, "secret")
	require.Error(t, err)
	assert.ErrorIs(t, err, fs.ErrNotExist)
}
