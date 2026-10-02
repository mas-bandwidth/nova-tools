package functional

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A package with a functional file is selected with exactly that file's
// tests; a package with none, and a directory with no Go at all, are not.
func TestSelectNamesOnlyTheTaggedTests(t *testing.T) {
	t.Parallel()

	mixed := filepath.Join("testdata", "mixed")
	got, err := Select([]string{filepath.Join("testdata", "plain"), mixed, filepath.Join("testdata", "absent")})
	require.NoError(t, err)
	want := []Package{{Dir: mixed, Tests: []string{"TestStoreRefuses", "TestStoreRoundTrip"}}}
	assert.Equal(t, want, got, "Select = %+v, want %+v", got, want)
	assert.Equal(t, "^(TestStoreRefuses|TestStoreRoundTrip)$", RunPattern(got), "RunPattern = %q, want %q", RunPattern(got), "^(TestStoreRefuses|TestStoreRoundTrip)$")
}

// A dir/... pattern is every package directory under it, testdata and dot
// directories excluded; any other argument is kept as given.
func TestExpandWalksTheTreeLikeGoList(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, d := range []string{"a/b", "a/testdata/x", "a/.hidden", "c"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}
	got, err := Expand([]string{filepath.Join(root, "a") + "/...", "./cmd/x"})
	require.NoError(t, err)
	want := []string{filepath.Join(root, "a"), filepath.Join(root, "a", "b"), "./cmd/x"}
	assert.Equal(t, want, got, "Expand = %q, want %q", got, want)
}

// Unmatched names each pattern go list would list nothing for, in order: a
// missing directory, a file, a directory with no .go file, a tree with none;
// a package directory and a tree holding one match.
func TestUnmatchedNamesEveryPatternWithNoPackage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, d := range []string{"empty/sub", "pkg"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}
	for _, f := range []string{"pkg/a.go", "file.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, f), []byte("package a\n"), 0o644))
	}
	j := func(p string) string { return filepath.Join(root, p) }
	got := Unmatched([]string{j("pkg"), root + "/...", j("nope"), j("nope") + "/...", j("file.txt"), j("empty"), j("empty") + "/..."})
	want := []string{
		`package pattern "` + j("nope") + `" matches no package (no such directory)`,
		`package pattern "` + j("nope") + `/..." matches no package (no such directory)`,
		`package pattern "` + j("file.txt") + `" matches no package (not a directory)`,
		`package pattern "` + j("empty") + `" matches no package (the directory holds no .go file)`,
		`package pattern "` + j("empty") + `/..." matches no package (no directory under it holds a .go file)`,
	}
	assert.Equal(t, want, got, "Unmatched =\n%q\nwant\n%q", got, want)
}
