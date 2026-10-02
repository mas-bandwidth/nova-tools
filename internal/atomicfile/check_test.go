package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Check, CheckAfterMkdirAll and CheckAppend refuse where the write would, and
// write nothing: each case's verdict is compared with the write's own on an
// identical tree.
func TestACheckRefusesWhereTheWriteWould(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		unwrit bool
		setup  func(t *testing.T, root string) string // returns the target path
	}{
		{"parent is a file", false, func(t *testing.T, root string) string {
			require.NoError(t, os.WriteFile(filepath.Join(root, "p"), nil, 0o644))
			return filepath.Join(root, "p", "f")
		}},
		{"parent is a link", false, func(t *testing.T, root string) string {
			require.NoError(t, os.Mkdir(filepath.Join(root, "real"), 0o755))
			require.NoError(t, os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "p")))
			return filepath.Join(root, "p", "f")
		}},
		{"parent not writable", true, func(t *testing.T, root string) string {
			dir := filepath.Join(root, "p")
			require.NoError(t, os.Mkdir(dir, 0o755))
			require.NoError(t, os.Chmod(dir, 0o500))
			t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
			return filepath.Join(dir, "f")
		}},
		{"target is a directory", false, func(t *testing.T, root string) string {
			require.NoError(t, os.MkdirAll(filepath.Join(root, "p", "f"), 0o755))
			return filepath.Join(root, "p", "f")
		}},
		{"parent not there", false, func(t *testing.T, root string) string { return filepath.Join(root, "p", "q", "f") }},
		{"a plain write", false, func(t *testing.T, root string) string { return filepath.Join(root, "f") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.unwrit && (runtime.GOOS == "windows" || os.Geteuid() == 0) {
				t.Skip("a directory without write permission cannot be made here")
			}
			real, plan := t.TempDir(), t.TempDir()
			target := tc.setup(t, real)
			werr := os.MkdirAll(filepath.Dir(target), 0o755)
			if werr == nil {
				werr = Write(target, []byte("x"), 0o644)
			}
			target = tc.setup(t, plan)
			before, _ := os.ReadDir(plan)
			cerr := CheckAfterMkdirAll(target, 0o644)
			after, _ := os.ReadDir(plan)
			assert.Equal(t, werr == nil, cerr == nil, "write: %v; check: %v", werr, cerr)
			assert.Equal(t, len(before), len(after), "the check wrote")
			if tc.name == "parent not there" {
				_, err := os.Stat(filepath.Dir(target))
				assert.True(t, os.IsNotExist(err), "the check made the parent")
			}
		})
	}
}

// CheckAppend admits an absent or writable regular file and refuses a
// directory at the name or a parent that is a file.
func TestCheckAppend(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	assert.NoError(t, CheckAppend(filepath.Join(root, "new.log")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "old.log"), nil, 0o644))
	assert.NoError(t, CheckAppend(filepath.Join(root, "old.log")))
	require.NoError(t, os.Mkdir(filepath.Join(root, "d"), 0o755))
	assert.Error(t, CheckAppend(filepath.Join(root, "d")))
	assert.Error(t, CheckAppend(filepath.Join(root, "old.log", "x")))
}
