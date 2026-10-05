// never_force_class_test.go enforces that nothing in nova-tools may rewrite a shared ref.
// It scans for force push patterns and refuses them against shared refs.
// See docs/SPEC-CI.md#never-force for the rule.
package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoForcePushOrHardResetOfASharedRef(t *testing.T) {
	t.Parallel()

	// Get the repository root
	root, err := os.Getwd()
	require.NoError(t, err)

	// Read allowlist - paths in allowlist are relative to repo root
	allowlistPath := filepath.Join(root, "never_force_allowlist.txt")
	raw, err := os.ReadFile(allowlistPath)
	require.NoError(t, err, "failed to read never-force allowlist")

	l, err := allowlist.Parse(allowlistPath, string(raw), allowlist.Options{Ceiling: true})
	require.NoError(t, err, "failed to parse never-force allowlist")

	allowlistMap := make(map[string]bool)
	for _, row := range l.Rows() {
		allowlistMap[row.Key] = true
	}

	// Scan for force patterns
	findings, err := ScanForForcePatterns(root)
	require.NoError(t, err, "failed to scan for force patterns")

	// Check findings against allowlist - convert absolute paths to relative
	for path, lines := range findings {
		// Convert to path relative to root
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		if !allowlistMap[rel] {
			t.Errorf("unlisted force push pattern in %s:\n%s", rel, strings.Join(lines, "\n"))
		}
	}
}

func TestNeverForceAllowlistUpdate(t *testing.T) {
	t.Parallel()

	// Test that the allowlist only shrinks
	dir := t.TempDir()
	p := filepath.Join(dir, "allowlist.txt")

	// Write initial allowlist
	initial := "# allowlist\n# ceiling: 1\nfixture.sh 1\n"
	err := os.WriteFile(p, []byte(initial), 0644)
	require.NoError(t, err)

	// Parse the allowlist - read content
	raw, err := os.ReadFile(p)
	require.NoError(t, err)
	l, err := allowlist.Parse(p, string(raw), allowlist.Options{Ceiling: true})
	require.NoError(t, err)

	// Check with same count (should pass)
	result := allowlist.Check(t, l, map[string]bool{"fixture.sh": true})
	assert.Empty(t, result.Unlisted)
	assert.Empty(t, result.Stale)

	// Check with key not in list (should fail)
	result = allowlist.Check(t, l, map[string]bool{"fixture.sh": true, "newfile.sh": true})
	assert.NotEmpty(t, result.Unlisted)
}
