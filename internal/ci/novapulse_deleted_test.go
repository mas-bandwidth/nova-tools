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
// gone, nothing in the tree runs the nova-pulse binary (internal/pulse, the frozen
// engine no command reaches, is the one package left to delete), no bench
// script installs or checks it, and docs/CLI.md's section says where its verbs went.
func TestTheNovaPulseCommandIsDeleted(t *testing.T) {
	root := repoRoot(t)
	for _, gone := range []string{"cmd/nova-pulse", "tools/nova-pulse-run.sh"} {
		if _, err := os.Stat(filepath.Join(root, gone)); err == nil {
			t.Errorf("%s still exists; nova-pulse is deleted (#3801)", gone)
		}
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == ".git" || rel == "internal/pulse" || strings.HasSuffix(rel, "/testdata") || rel == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
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
	for _, script := range []string{"tools/bench-standard.sh", "tools/bench-standard_test.sh"} {
		b, err := os.ReadFile(filepath.Join(root, script))
		if err != nil {
			t.Fatal(err)
		}
		for n, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if strings.Contains(line, "nova-pulse") {
				t.Errorf("%s:%d still installs or checks nova-pulse: %s", script, n+1, strings.TrimSpace(line))
			}
		}
	}
	doc, err := os.ReadFile(filepath.Join(root, "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(doc), "\n## nova-pulse\n")
	if !ok {
		t.Fatal("docs/CLI.md has no ## nova-pulse section saying where its verbs went")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	for _, want := range []string{"Deleted (nova-tools #3801)", "nova-sprint card cut", "nova-sprint card harvest", "nova-sprint table"} {
		if !strings.Contains(section, want) {
			t.Errorf("docs/CLI.md's nova-pulse section does not say %q", want)
		}
	}
	if strings.Contains(section, "\nnova-pulse ") {
		t.Error("docs/CLI.md's nova-pulse section still gives a synopsis for the deleted binary")
	}
}
