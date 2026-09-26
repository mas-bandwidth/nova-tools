package functional

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
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
