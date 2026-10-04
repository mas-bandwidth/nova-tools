package testkit

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDryrunCoverSnapshot pins the snapshot's main path: every path under the
// root is recorded by mode, a regular file also by its bytes, and a symlink by
// its target and not followed.
func TestDryrunCoverSnapshot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	WriteFile(t, filepath.Join(root, "a.txt"), "hello")
	require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0o755))
	WriteFile(t, filepath.Join(root, "sub", "b.txt"), "world")
	require.NoError(t, os.Symlink("a.txt", filepath.Join(root, "link")))

	got := Snapshot(t, root)
	assert.Equal(t, "-rw-r--r-- hello", got[filepath.Join(root, "a.txt")])
	assert.Equal(t, "drwxr-xr-x", got[filepath.Join(root, "sub")])
	assert.Equal(t, "-rw-r--r-- world", got[filepath.Join(root, "sub", "b.txt")])
	link, err := os.Lstat(filepath.Join(root, "link"))
	require.NoError(t, err)
	assert.Equal(t, link.Mode().String()+" -> a.txt", got[filepath.Join(root, "link")])
}

// TestDryrunCoverSnapshotRefusesAnUnreadableDirectory pins the snapshot's
// refusal: a directory the walker cannot read is recorded as unreadable and
// not descended into, so the call does not fail the test.
func TestDryrunCoverSnapshotRefusesAnUnreadableDirectory(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a directory without read permission cannot be made here")
	}
	root := t.TempDir()
	sub := filepath.Join(root, "closed")
	require.NoError(t, os.Mkdir(sub, 0o755))
	require.NoError(t, os.Chmod(sub, 0o000))
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })

	assert.Equal(t, "unreadable", Snapshot(t, root)[sub])
}

// TestDryrunCoverUnwritable pins the unwritable helper's main path: the
// directory is left at 0o500 for the rest of the test, so a write into it is
// refused, and the cleanup restores a mode that lets the temp tree go.
func TestDryrunCoverUnwritable(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a directory without write permission cannot be made here")
	}
	dir := t.TempDir()
	Unwritable(t, dir)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o500), info.Mode().Perm())
	assert.Error(t, os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o644),
		"a write into the unwritable directory was not refused")
}

// TestDryrunCoverAgrees pins the agreement helper: a dry run that refuses like
// the real run and leaves its tree byte-identical passes, for both a success
// and a refusal.
func TestDryrunCoverAgrees(t *testing.T) {
	t.Parallel()
	run := func(args ...string) Result {
		switch args[0] {
		case "ok":
			return Result{Code: 0, Stdout: "ok: wrote\n"}
		case "refuse":
			return Result{Code: 1, Stderr: "refuse REFUSED: nothing to do; run: refuse --help\n"}
		}
		return Result{Code: 2, Stderr: "unknown\n"}
	}
	DryRunAgrees(t, run, []DryCase{
		{Name: "success", Setup: func(t *testing.T, root string) ([]string, []string) {
			WriteFile(t, filepath.Join(root, "in.txt"), "x")
			return []string{"ok"}, []string{"--root", root}
		}},
		{Name: "refusal", Setup: func(t *testing.T, root string) ([]string, []string) {
			return []string{"refuse"}, []string{"--root", root}
		}},
	})
}

// TestDryrunCoverStatusWords pins the status-word reading: OK for a success,
// the words through FAIL, NO or REFUSED on the line that carries it, a NOTE
// line skipped as commentary, and no status word when a failure says none.
func TestDryrunCoverStatusWords(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		r    Result
		want string
	}{
		{"success", Result{Code: 0}, "OK"},
		{"fail", Result{Code: 1, Stderr: "build FAIL: broken\n"}, "build FAIL:"},
		{"no", Result{Code: 1, Stdout: "check NO such thing\n"}, "check NO"},
		{"refused", Result{Code: 2, Stderr: "tool REFUSED: bad flag; run: tool --help\n"}, "tool REFUSED:"},
		{"note before the answer", Result{Code: 1, Stderr: "NOTE: thinking\nverb NO: reason\n"}, "verb NO:"},
		{"no status word", Result{Code: 1, Stderr: "something went wrong\n"}, "no status word"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, statusWords(c.r))
		})
	}
}
