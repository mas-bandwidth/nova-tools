package ci

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// novaPulseRun is a Go line that runs the nova-pulse binary: an exec of it, or a
// flag whose default is it.
var novaPulseRun = regexp.MustCompile(`(Command(Context)?\(|\.String\()[^\n]*"nova-pulse"`)

// TestTheNovaPulseCommandIsDeleted is the DONE-WHEN of nova-tools #3801: cmd/nova-pulse is
// gone, nothing in the tree runs the nova-pulse binary, no bench script installs
// or checks it, and living command references omit it.
func TestTheNovaPulseCommandIsDeleted(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, gone := range []string{"cmd/nova-pulse", "tools/nova-pulse-run.sh"} {
		if _, err := os.Stat(filepath.Join(root, gone)); err == nil {
			t.Errorf("%s still exists; nova-pulse is deleted (#3801)", gone)
		}
	}
	err := walkSourceDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == ".git" || strings.HasSuffix(rel, "/testdata") || rel == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		b, err := readSourceFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(b), `"nova-pulse"`) {
			return nil // every novaPulseRun match names the binary in quotes
		}
		for n, line := range strings.Split(string(b), "\n") {
			if novaPulseRun.MatchString(line) {
				t.Errorf("%s:%d runs the deleted nova-pulse binary: %s", rel, n+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The bench standard's witness does not install or check nova-pulse either:
	// every line of its Go, tests included, that is not a comment.
	witness, err := filepath.Glob(filepath.Join(root, "tools", "benchstandard", "*.go"))
	if err != nil || len(witness) == 0 {
		t.Fatalf("no Go files in tools/benchstandard: %v", err)
	}
	for _, path := range witness {
		script, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for n, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if strings.Contains(line, "nova-pulse") {
				t.Errorf("%s:%d still installs or checks nova-pulse: %s", script, n+1, strings.TrimSpace(line))
			}
		}
	}
	for _, name := range []string{"CLI.md", "TESTS.md"} {
		doc, err := os.ReadFile(filepath.Join(root, "docs", name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(doc), "nova-pulse") {
			t.Errorf("docs/%s names nova-pulse; living references must omit deleted tools", name)
		}
	}
}
