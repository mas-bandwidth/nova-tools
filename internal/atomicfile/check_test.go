package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
		{"name too long, parent not there", false, func(t *testing.T, root string) string {
			return filepath.Join(root, "p", strings.Repeat("x", 242))
		}},
		{"target is a dangling link", false, func(t *testing.T, root string) string {
			require.NoError(t, os.Mkdir(filepath.Join(root, "p"), 0o755))
			require.NoError(t, os.Symlink(filepath.Join(root, "nowhere"), filepath.Join(root, "p", "f")))
			return filepath.Join(root, "p", "f")
		}},
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

// CheckAfterMkdirAll makes the write's checks of the path and the mode even
// when the parent is not there yet.
func TestCheckAfterMkdirAllChecksTheNameWhenTheParentIsMissing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	assert.NoError(t, CheckAfterMkdirAll(filepath.Join(missing, "f"), 0o644))
	assert.ErrorContains(t, CheckAfterMkdirAll(filepath.Join(missing, strings.Repeat("x", 242)), 0o644), "name too long")
	assert.ErrorContains(t, CheckAfterMkdirAll(filepath.Join(missing, "f"), 0o1777), "unsupported file mode")
	assert.ErrorContains(t, CheckAfterMkdirAll(missing+"/./f", 0o644), "not clean")
	_, err := os.Stat(missing)
	assert.True(t, os.IsNotExist(err), "a check made the parent")
}

// CheckAppend judges a link by what an append through it meets: os.OpenFile
// with O_CREATE follows the link and creates its referent in a directory that
// must be there. Each case compares the check with the append itself on an
// identical tree.
func TestCheckAppendFollowsALinkAsTheAppendDoes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, root string) string
	}{
		{"relative missing component before dotdot", func(t *testing.T, root string) string {
			p := filepath.Join(root, "log")
			require.NoError(t, os.Symlink("missing/../new.log", p))
			return p
		}},
		{"relative symlink component before dotdot", func(t *testing.T, root string) string {
			require.NoError(t, os.MkdirAll(filepath.Join(root, "real", "d"), 0o755))
			require.NoError(t, os.Symlink(filepath.Join(root, "real", "d"), filepath.Join(root, "via")))
			// The kernel reaches real/new.log; lexical cleaning reaches root/new.log,
			// a directory, so it incorrectly refuses the append.
			require.NoError(t, os.Mkdir(filepath.Join(root, "new.log"), 0o755))
			p := filepath.Join(root, "log")
			require.NoError(t, os.Symlink("via/../new.log", p))
			return p
		}},
		{"relative link chain keeps dotdot", func(t *testing.T, root string) string {
			require.NoError(t, os.MkdirAll(filepath.Join(root, "real", "d"), 0o755))
			require.NoError(t, os.Symlink(filepath.Join(root, "real", "d"), filepath.Join(root, "via")))
			require.NoError(t, os.Symlink("missing/../new.log", filepath.Join(root, "real", "next")))
			p := filepath.Join(root, "log")
			require.NoError(t, os.Symlink("via/../next", p))
			return p
		}},
		{"relative link cycle", func(t *testing.T, root string) string {
			p := filepath.Join(root, "log")
			require.NoError(t, os.Symlink("next", p))
			require.NoError(t, os.Symlink("log", filepath.Join(root, "next")))
			return p
		}},
		{"link into a missing directory", func(t *testing.T, root string) string {
			p := filepath.Join(root, "log")
			require.NoError(t, os.Symlink(filepath.Join(root, "missing", "log"), p))
			return p
		}},
		{"dangling link in a directory that is there", func(t *testing.T, root string) string {
			require.NoError(t, os.Mkdir(filepath.Join(root, "d"), 0o755))
			p := filepath.Join(root, "log")
			require.NoError(t, os.Symlink(filepath.Join(root, "d", "log"), p))
			return p
		}},
		{"link to a regular file", func(t *testing.T, root string) string {
			require.NoError(t, os.WriteFile(filepath.Join(root, "real"), nil, 0o644))
			p := filepath.Join(root, "log")
			require.NoError(t, os.Symlink(filepath.Join(root, "real"), p))
			return p
		}},
		{"link to a directory", func(t *testing.T, root string) string {
			require.NoError(t, os.Mkdir(filepath.Join(root, "d"), 0o755))
			p := filepath.Join(root, "log")
			require.NoError(t, os.Symlink(filepath.Join(root, "d"), p))
			return p
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			real, plan := t.TempDir(), t.TempDir()
			p := tc.setup(t, real)
			f, werr := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
			if werr == nil {
				_, err := f.WriteString("append")
				require.NoError(t, err)
				require.NoError(t, f.Close())
			}
			p = tc.setup(t, plan)
			link, _ := os.Readlink(p)
			before := appendTree(t, plan)
			cerr := CheckAppend(p)
			assert.Equal(t, before, appendTree(t, plan), "the check changed the tree")
			assert.Equal(t, werr == nil, cerr == nil, "append: %v; check: %v", werr, cerr)
			after, _ := os.Readlink(p)
			assert.Equal(t, link, after, "the check changed the link")
			_, err := os.Stat(filepath.Join(plan, "missing"))
			assert.True(t, os.IsNotExist(err), "the check made a directory")
		})
	}
}

// appendTree records directories, link texts and file bytes without following links.
func appendTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	require.NoError(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			text, err := os.Readlink(path)
			if err != nil {
				return err
			}
			out[rel] = "link:" + text
		case entry.IsDir():
			out[rel] = "dir"
		default:
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[rel] = "file:" + string(data)
		}
		return nil
	}))
	return out
}
