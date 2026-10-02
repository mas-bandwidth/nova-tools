package functional

import (
	"os"
	"path/filepath"
	"reflect"
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
	if err != nil {
		t.Fatal(err)
	}
	want := []Package{{Dir: mixed, Tests: []string{"TestStoreRefuses", "TestStoreRoundTrip"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Select = %+v, want %+v", got, want)
	}
	if got, want := RunPattern(got), "^(TestStoreRefuses|TestStoreRoundTrip)$"; got != want {
		t.Errorf("RunPattern = %q, want %q", got, want)
	}
}

// A dir/... pattern is every package directory under it, testdata and dot
// directories excluded; any other argument is kept as given.
func TestExpandWalksTheTreeLikeGoList(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, d := range []string{"a/b", "a/testdata/x", "a/.hidden", "c"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Expand([]string{filepath.Join(root, "a") + "/...", "./cmd/x"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "a"), filepath.Join(root, "a", "b"), "./cmd/x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Expand = %q, want %q", got, want)
	}
}

// A ./-rooted pattern prints ./-rooted directories, `./...` itself included:
// `go test` reads a bare `func` as a standard-library import path and refuses
// it, so `./...` once printed `fast func` and the functional tier failed to set
// up.
func TestExpandKeepsTheDotSlashOfEveryRelativePattern(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	for _, d := range []string{"fast", "func/inner", "testdata/x"} {
		require.NoError(t, os.MkdirAll(filepath.Join(cwd, d), 0o755))
	}
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"the whole module", []string{"./..."}, []string{".", "./fast", "./func", "./func/inner"}},
		{"a subtree", []string{"./func/..."}, []string{"./func", "./func/inner"}},
		{"a bare subtree", []string{"func/..."}, []string{"func", "func/inner"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := expandIn(cwd, tc.args)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// Unmatched names each pattern go list would list nothing for, in order: a
// missing directory, a file, a directory with no .go file, a tree with none;
// a package directory and a tree holding one match.
func TestUnmatchedNamesEveryPatternWithNoPackage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, d := range []string{"empty/sub", "pkg"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"pkg/a.go", "file.txt"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("package a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
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
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Unmatched =\n%q\nwant\n%q", got, want)
	}
}
