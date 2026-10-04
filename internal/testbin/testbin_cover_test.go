package testbin

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTestbinCoverPlaceCopy pins PlaceCopy's main path: a real byte copy lands
// at dst with the executable bit set, replacing a dst already there, and never
// shares src's inode -- a link would answer the "is my parent this executable?"
// guard yes and silently stop testing anything.
func TestTestbinCoverPlaceCopy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		dst     bool
	}{
		{name: "dst absent", content: "fresh\n"},
		{name: "dst replaced", content: "new\n", dst: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			src := filepath.Join(dir, "src")
			dst := filepath.Join(dir, "dst")
			require.NoError(t, os.WriteFile(src, []byte(tc.content), 0o755))
			if tc.dst {
				require.NoError(t, os.WriteFile(dst, []byte("old\n"), 0o644))
			}

			require.NoError(t, PlaceCopy(src, dst))

			got, err := os.ReadFile(dst)
			require.NoError(t, err)
			assert.Equal(t, tc.content, string(got), "dst content = %q, want %q", got, tc.content)

			si, err := os.Stat(src)
			require.NoError(t, err)
			di, err := os.Stat(dst)
			require.NoError(t, err)
			assert.False(t, os.SameFile(si, di), "PlaceCopy linked instead of copying: %s and %s share an inode", src, dst)
			if runtime.GOOS != "windows" {
				assert.NotZero(t, di.Mode().Perm()&0o111, "the copied mode %v has no execute bit", di.Mode().Perm())
			}
		})
	}
}

// TestTestbinCoverPlaceCopyRefusesMissingSource pins the refusal: a src that
// cannot be read is an error, not a silent empty file at dst.
func TestTestbinCoverPlaceCopyRefusesMissingSource(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "absent")
	dst := filepath.Join(dir, "dst")

	err := PlaceCopy(src, dst)

	require.Error(t, err)
	assert.NoFileExists(t, dst, "PlaceCopy wrote dst despite an unreadable src")
}
