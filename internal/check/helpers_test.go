package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
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
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), mode))
	// Ensure the mode sticks regardless of umask.
	require.NoError(t, os.Chmod(path, mode))
}

// wantFailures asserts that each want substring appears in some failure's
// Subject+Reason, and that an empty want means no failures at all.
func wantFailures(t *testing.T, failures []Failure, want []string) {
	t.Helper()
	if len(want) == 0 {
		require.Empty(t, failures, "expected no failures, got %v", failures)
		return
	}
	require.NotEmpty(t, failures, "expected failures containing %v, got none", want)
	for _, w := range want {
		found := false
		for _, f := range failures {
			if strings.Contains(f.Subject+": "+f.Reason, w) {
				found = true
				break
			}
		}
		assert.True(t, found, "no failure contains %q; failures: %v", w, failures)
	}
}

// brief renders a value for a test failure the way the tools render one for a
// caller: one line, escaped, and capped, so a large or hostile string in a
// fixture cannot turn a failure message into a screenful.
func brief(s string) string { return oneline.Escape(oneline.Cap(s, 200)) }
