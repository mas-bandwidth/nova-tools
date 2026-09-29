package tlc

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Every non-test Go file of the package says whether it decides how a result is
// produced and read (ResultFiles: in every fingerprint) or not
// (BookkeepingFiles: in none), in exactly one of the two lists, so a file added
// later cannot be left out of the rule or counted twice.
func TestEveryRunnerFileIsClassified(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var dir []string
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			dir = append(dir, n)
		}
	}
	listed := map[string]int{}
	for _, n := range ResultFiles {
		listed[n]++
	}
	for _, n := range BookkeepingFiles {
		listed[n]++
	}
	for n, times := range listed {
		if times != 1 {
			t.Errorf("%s is in ResultFiles and BookkeepingFiles %d times, want exactly one list once", n, times)
		}
	}
	for _, n := range dir {
		if listed[n] == 0 {
			t.Errorf("%s is in neither ResultFiles nor BookkeepingFiles: say in fingerprint.go whether it decides how a result is produced and read", n)
		}
		delete(listed, n)
	}
	for n := range listed {
		t.Errorf("%s is listed in fingerprint.go and is not a non-test file of the package", n)
	}
}

// The binary carries the bytes of the result files it was built from, and no
// others.
func TestEmbeddedSourcesAreTheResultFiles(t *testing.T) {
	t.Parallel()
	files, err := RunnerFiles()
	if err != nil {
		t.Fatal(err)
	}
	var got, want []string
	for name, raw := range files {
		got = append(got, name)
		disk, err := os.ReadFile(filepath.Join(".", filepath.Base(name)))
		if err != nil || string(disk) != string(raw) {
			t.Errorf("%s: the embedded bytes are not the file's", name)
		}
	}
	for _, n := range ResultFiles {
		want = append(want, RunnerDir+"/"+n)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("embedded %v, ResultFiles names %v: fix the go:embed line", got, want)
	}
	entries, _ := sources.ReadDir(".")
	if len(entries) != len(ResultFiles) {
		t.Fatalf("the embedded directory holds %d files, ResultFiles %d", len(entries), len(ResultFiles))
	}
}

func TestCheckRunnerComparesTheEmbeddedResultFilesWithTheRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := CheckRunner(root); err != nil {
		t.Fatalf("a root with no runner sources: %v", err)
	}
	if err := CheckRunner(filepath.Join("..", "..")); err != nil {
		t.Fatalf("this repository: %v", err)
	}
	dir := filepath.Join(root, RunnerDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	built, _ := RunnerFiles()
	for path, raw := range built {
		if err := os.WriteFile(filepath.Join(root, path), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := CheckRunner(root); err != nil {
		t.Fatalf("the same files: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "suite.go"), []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := CheckRunner(root)
	if err == nil || !strings.Contains(err.Error(), "internal/tlc/suite.go differ") || !strings.Contains(err.Error(), "build tlacheck from this tree") {
		t.Fatalf("an edited result file: %v", err)
	}
}
