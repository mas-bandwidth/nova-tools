package tlc

import (
	"os"
	"path/filepath"
	"slices"
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

// The binary carries the bytes of the files CheckRunner holds to the checkout
// (the result files and the input-list files), and no others; the fingerprint
// takes only the result files of them.
func TestEmbeddedSourcesAreTheCheckedFiles(t *testing.T) {
	t.Parallel()
	files, err := CheckedSources()
	if err != nil {
		t.Fatal(err)
	}
	if runner, err := RunnerFiles(); err != nil || len(runner) != len(ResultFiles) {
		t.Fatalf("the fingerprint takes %d runner files, %d are result files (%v)", len(runner), len(ResultFiles), err)
	}
	for _, n := range InputListFiles {
		if !slices.Contains(BookkeepingFiles, n) || slices.Contains(ResultFiles, n) {
			t.Errorf("%s is an input-list file: it is bookkeeping and no result file", n)
		}
	}
	var got, want []string
	for name, raw := range files {
		got = append(got, name)
		disk, err := os.ReadFile(filepath.Join(".", filepath.Base(name)))
		if err != nil || string(disk) != string(raw) {
			t.Errorf("%s: the embedded bytes are not the file's", name)
		}
	}
	for _, n := range CheckedFiles() {
		want = append(want, RunnerDir+"/"+n)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("embedded %v, CheckedFiles names %v: fix the go:embed line", got, want)
	}
	entries, _ := sources.ReadDir(".")
	if len(entries) != len(CheckedFiles()) {
		t.Fatalf("the embedded directory holds %d files, CheckedFiles %d", len(entries), len(CheckedFiles()))
	}
}

func TestCheckRunnerComparesTheEmbeddedCheckedFilesWithTheRoot(t *testing.T) {
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
	built, _ := CheckedSources()
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
	if err := os.WriteFile(filepath.Join(dir, "suite.go"), built[RunnerDir+"/suite.go"], 0o644); err != nil {
		t.Fatal(err)
	}
	// inputs.go computes the list of files a case reads: a binary built from
	// another one computes other inputs than the checkout does.
	if err := os.WriteFile(filepath.Join(dir, "inputs.go"), []byte("another input list\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = CheckRunner(root)
	if err == nil || !strings.Contains(err.Error(), "internal/tlc/inputs.go differ") || strings.Contains(err.Error(), "suite.go") {
		t.Fatalf("an edited input-list file: %v", err)
	}
	// Any other bookkeeping file decides nothing about the inputs.
	if err := os.WriteFile(filepath.Join(dir, "inputs.go"), built[RunnerDir+"/inputs.go"], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "records.go"), []byte("another bookkeeping file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckRunner(root); err != nil {
		t.Fatalf("an edited bookkeeping file: %v", err)
	}
}
