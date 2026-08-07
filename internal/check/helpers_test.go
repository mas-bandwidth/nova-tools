package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree materializes a map of relative path -> content under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		writeMode(t, dir, rel, content, 0o644)
	}
}

func writeMode(t *testing.T, dir, rel, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// Ensure the mode sticks regardless of umask.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// wantFailures asserts that each want substring appears in some failure's
// Subject+Reason, and that an empty want means no failures at all.
func wantFailures(t *testing.T, failures []Failure, want []string) {
	t.Helper()
	if len(want) == 0 {
		if len(failures) != 0 {
			t.Fatalf("expected no failures, got %v", failures)
		}
		return
	}
	if len(failures) == 0 {
		t.Fatalf("expected failures containing %v, got none", want)
	}
	for _, w := range want {
		found := false
		for _, f := range failures {
			if strings.Contains(f.Subject+": "+f.Reason, w) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no failure contains %q; failures: %v", w, failures)
		}
	}
}
