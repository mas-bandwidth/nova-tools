package tlc

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The embedded list is written by hand because go:embed cannot leave the tests
// out; it must be exactly the directory's non-test Go files, or a runner file
// would change what a result means without changing the fingerprint.
func TestEmbeddedSourcesAreTheNonTestFiles(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			want = append(want, RunnerDir+"/"+n)
		}
	}
	files, err := RunnerFiles()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for name, raw := range files {
		got = append(got, name)
		disk, err := os.ReadFile(filepath.Join(".", filepath.Base(name)))
		if err != nil || string(disk) != string(raw) {
			t.Errorf("%s: the embedded bytes are not the file's", name)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("embedded %v, directory holds %v: add the file to the go:embed line", got, want)
	}
}
