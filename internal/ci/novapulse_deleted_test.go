package ci

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		_, err := os.Stat(filepath.Join(root, gone))
		assert.Error(t, err, "%s still exists; nova-pulse is deleted (#3801)", gone)
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
			assert.False(t, novaPulseRun.MatchString(line), "%s:%d runs the deleted nova-pulse binary: %s", rel, n+1, strings.TrimSpace(line))
		}
		return nil
	})
	require.NoError(t, err)
	// The bench standard's witness does not install or check nova-pulse either:
	// every line of its Go, tests included, that is not a comment.
	witness, err := filepath.Glob(filepath.Join(root, "tools", "benchstandard", "*.go"))
	require.NoError(t, err, "no Go files in tools/benchstandard: %v", err)
	require.NotEmpty(t, witness, "no Go files in tools/benchstandard: %v", err)
	for _, path := range witness {
		script, err := filepath.Rel(root, path)
		require.NoError(t, err)
		b, err := os.ReadFile(path)
		require.NoError(t, err)
		for n, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			assert.NotContains(t, line, "nova-pulse", "%s:%d still installs or checks nova-pulse: %s", script, n+1, strings.TrimSpace(line))
		}
	}
	for _, name := range []string{"CLI.md", "TESTS.md"} {
		doc, err := os.ReadFile(filepath.Join(root, "docs", name))
		require.NoError(t, err)
		assert.NotContains(t, string(doc), "nova-pulse", "docs/%s names nova-pulse; living references must omit deleted tools", name)
	}
}
