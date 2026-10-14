//go:build unix

package release

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A backslash is part of a Unix filename. A neighbouring symlink whose name
// omits it must not make creating or replacing that regular file fail.
func TestNoFollowPreservesLiteralTrailingBackslashes(t *testing.T) {
	t.Parallel()
	for _, suffix := range []string{"\\", "\\\\"} {
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			link, outside := plantSymlink(t, dir, "output", "outside unchanged")
			path := link + suffix
			for _, body := range []string{"created", "replaced"} {
				err := writeNoFollow("write", path, []byte(body), 0644)
				require.NoError(t, err, "literal path %q refused: %v", path, err)
				got, err := os.ReadFile(path)
				require.NoError(t, err, "exact path bytes=%q err=%v", got, err)
				require.Equal(t, body, string(got), "exact path bytes=%q err=%v", got, err)
			}
			got, err := os.ReadFile(outside)
			require.NoError(t, err, "neighbour target=%q err=%v", got, err)
			require.Equal(t, "outside unchanged", string(got), "neighbour target=%q err=%v", got, err)
			target, err := os.Readlink(link)
			require.NoError(t, err, "neighbour link=%q err=%v", target, err)
			require.Equal(t, outside, target, "neighbour link=%q err=%v", target, err)
		})
	}
}

// Only actual path separators may be stripped for Lstat: a literal backslash
// immediately before a slash still names the link itself on Unix.
func TestNoFollowNamesLiteralBackslashSymlinkBeforeSlash(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	link, outside := plantSymlink(t, dir, "output\\", "outside unchanged")
	err := writeNoFollow("write", link+string(filepath.Separator), []byte("no"), 0644)
	assertSymlinkRefused(t, "write", link, outside, "outside unchanged", err)
}
