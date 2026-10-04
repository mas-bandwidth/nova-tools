package check

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpellingCoverPathEnteredRoot pins pathEnteredRoot in
// internal/check/spelling.go:720. The function walks a path component by
// component, evaluating each through filepath.EvalSymlinks, and reports whether
// any resolved component lands under the caller's already-resolved root. It is
// the seam for the case where the caller's spelling of a path differs from the
// resolved root (e.g. /tmp versus /private/tmp) but the walk still threads
// through root. These tests build a real tree in t.TempDir and create symlinks
// through the package's own os seam; there is no subprocess, no network and no
// live store. Each row pins one branch of the function:
//   - inside: a path that lives entirely under root (main path, ends true).
//   - outside: a path that never resolves under root (refusal, ends false).
//   - escape: a path that enters root then leaves it through a symlink (ends true).
//   - missing_outside: EvalSymlinks errors before root is ever entered (seen is
//     still false, ends false).
//   - missing_inside: EvalSymlinks errors after root is entered (seen is true,
//     ends true).
func TestSpellingCoverPathEnteredRoot(t *testing.T) {
	t.Parallel()
	base := t.TempDir()

	root := filepath.Join(base, "root", "sub")
	require.NoError(t, os.MkdirAll(root, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.md"), []byte("ok\n"), 0o600))

	outside := filepath.Join(base, "elsewhere")
	require.NoError(t, os.MkdirAll(outside, 0o700))
	outsideFile := filepath.Join(outside, "file.md")
	require.NoError(t, os.WriteFile(outsideFile, []byte("ok\n"), 0o600))

	// escape.md lives inside root but points at a file outside root, exercising
	// the "entered then left" branch.
	escape := filepath.Join(root, "escape.md")
	require.NoError(t, os.Symlink(outsideFile, escape))

	rootResolved, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	require.Equal(t, root, rootResolved, "root should resolve to itself")

	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "inside",
			path:     filepath.Join(root, "file.md"),
			expected: true,
		},
		{
			name:     "outside",
			path:     outsideFile,
			expected: false,
		},
		{
			name:     "escape",
			path:     escape,
			expected: true,
		},
		{
			name:     "missing_outside",
			path:     filepath.Join(outside, "missing.md"),
			expected: false,
		},
		{
			name:     "missing_inside",
			path:     filepath.Join(root, "missing.md"),
			expected: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := pathEnteredRoot(rootResolved, tc.path)
			assert.Equal(t, tc.expected, got, "pathEnteredRoot(%q) = %v, want %v", tc.path, got, tc.expected)
		})
	}
}
