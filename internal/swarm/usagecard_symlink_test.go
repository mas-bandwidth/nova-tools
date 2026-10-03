package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// usage.tsv sits in a directory the card can write. A symlink at the name, or a
// parent that is a symlink, must not receive the row.
func TestAppendCardUsageRefusesASymlink(t *testing.T) {
	t.Parallel()

	const stay = "stay-put\n"
	t.Run("file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside")
		require.NoError(t, os.WriteFile(outside, []byte(stay), 0o644))
		link := filepath.Join(dir, "usage.tsv")
		plantSymlink(t, outside, link)
		err := AppendCardUsage(link, UsageRow{"job": "one"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "symlink")
		raw, err := os.ReadFile(outside)
		require.NoError(t, err)
		assert.Equal(t, stay, string(raw), "the append followed the symlink")
		fi, err := os.Lstat(link)
		require.NoError(t, err)
		assert.NotZero(t, fi.Mode()&os.ModeSymlink, "the planted link was replaced")
	})
	t.Run("directory", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		outside := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(outside, "kept"), []byte(stay), 0o644))
		link := filepath.Join(root, "job")
		plantSymlink(t, outside, link)
		before := dirNames(t, outside)
		err := AppendCardUsage(filepath.Join(link, "usage.tsv"), UsageRow{"job": "one"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "symlink")
		assert.Equal(t, before, dirNames(t, outside), "the append followed the directory symlink")
		fi, err := os.Lstat(link)
		require.NoError(t, err)
		assert.NotZero(t, fi.Mode()&os.ModeSymlink, "the planted link was replaced")
	})
}

func TestAppendCardUsageAppendsToARegularFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "usage.tsv")
	require.NoError(t, AppendCardUsage(path, UsageRow{"job": "one"}))
	require.NoError(t, AppendCardUsage(path, UsageRow{"job": "two"}))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	require.Len(t, lines, 3, "the file is header plus two rows, got:\n%s", raw)
	assert.True(t, strings.HasPrefix(lines[0], "job\t"), "the first line is the header, got %q", lines[0])
	assert.Contains(t, lines[1], "one")
	assert.Contains(t, lines[2], "two")
	assert.Equal(t, 1, strings.Count(string(raw), "job\t"), "the header was written again on the second append")
}
