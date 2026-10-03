package swarm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func plantSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this filesystem will not make a symlink: %v", err)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// A planted symlink is not a place this write may publish. The bytes outside
// stay what they were, and the link itself stays a link.
func TestWriteFileNoFollowRefusesASymlink(t *testing.T) {
	t.Parallel()

	const stay = "stay-put\n"
	cases := []struct {
		name  string
		plant func(t *testing.T, root, outside string) string
	}{
		{
			name: "file",
			plant: func(t *testing.T, root, outside string) string {
				t.Helper()
				target := filepath.Join(outside, "kept")
				require.NoError(t, os.WriteFile(target, []byte(stay), 0o644))
				link := filepath.Join(root, "auth.json")
				plantSymlink(t, target, link)
				return link
			},
		},
		{
			name: "directory",
			plant: func(t *testing.T, root, outside string) string {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(outside, "kept"), []byte(stay), 0o644))
				link := filepath.Join(root, "opencode")
				plantSymlink(t, outside, link)
				return filepath.Join(link, "auth.json")
			},
		},
		{
			name: "ancestor",
			plant: func(t *testing.T, root, outside string) string {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(outside, "kept"), []byte(stay), 0o644))
				link := filepath.Join(root, ".config")
				plantSymlink(t, outside, link)
				return filepath.Join(link, "opencode", "opencode.json")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			outside := t.TempDir()
			path := tc.plant(t, root, outside)
			before := dirNames(t, outside)
			err := WriteFileNoFollow(root, path, []byte("inside\n"), 0o600)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "symlink")
			assert.Equal(t, before, dirNames(t, outside), "the write followed the symlink and changed %s", outside)
			raw, err := os.ReadFile(filepath.Join(outside, "kept"))
			require.NoError(t, err)
			assert.Equal(t, stay, string(raw))
			link := map[string]string{
				"file":      filepath.Join(root, "auth.json"),
				"directory": filepath.Join(root, "opencode"),
				"ancestor":  filepath.Join(root, ".config"),
			}[tc.name]
			fi, err := os.Lstat(link)
			require.NoError(t, err)
			assert.NotZero(t, fi.Mode()&os.ModeSymlink, "the planted link was replaced")
		})
	}
}

func TestWriteFileNoFollowCreatesARegularFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, ".config", "opencode", "opencode.json")
	require.NoError(t, WriteFileNoFollow(root, path, []byte("inside\n"), 0o600))
	fi, err := os.Lstat(path)
	require.NoError(t, err)
	assert.True(t, fi.Mode().IsRegular(), "the published file is not regular: %s", fi.Mode())
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "inside\n", string(raw))
}
