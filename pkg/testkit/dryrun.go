package testkit

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Snapshot is every path under root with its mode and, for a regular file, its
// bytes; a link is recorded by its target and not followed. Two snapshots are
// equal only when nothing under root was written, created or removed.
func Snapshot(t testing.TB, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsPermission(err) {
				out[path] = "unreadable"
				return fs.SkipDir
			}
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		v := info.Mode().String()
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			v += " -> " + target
		case d.Type().IsRegular():
			raw, err := os.ReadFile(path)
			if err == nil {
				v += " " + string(raw)
			}
		}
		out[path] = v
		return nil
	}))
	return out
}

// DryCase is one bad input for a verb that writes: Setup lays it out under a
// fresh root and returns the verb's words and the rest of its arguments, so the
// case runs as `<verb> --dry-run <rest>` and as `<verb> <rest>`.
type DryCase struct {
	Name     string
	Unwrites bool // the case takes a directory's write permission away (skipped on Windows and as root)
	Setup    func(t *testing.T, root string) (verb, rest []string)
}

// Unwritable takes the write permission off dir for the rest of the test.
func Unwritable(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() {
		// ignored: restoring the mode only lets TempDir's cleanup remove the tree; a failure there is reported by it
		_ = os.Chmod(dir, 0o755)
	})
}

// DryRunAgrees holds a verb's --dry-run to its real run: on each case the dry
// run exits as the real run does, with the same status words opening its first
// line (a refusal refuses, a failure fails), and leaves the tree under its root
// byte-identical. The two runs get identical fresh trees, so the real run's
// writes cannot change what the dry run sees.
func DryRunAgrees(t *testing.T, run func(args ...string) Result, cases []DryCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.Unwrites && (runtime.GOOS == "windows" || os.Geteuid() == 0) {
				t.Skip("a directory without write permission cannot be made here")
			}
			realRoot, dryRoot := t.TempDir(), t.TempDir()
			verb, rest := c.Setup(t, realRoot)
			real := run(append(append([]string{}, verb...), rest...)...)
			verb, rest = c.Setup(t, dryRoot)
			before := Snapshot(t, dryRoot)
			dry := run(append(append(append([]string{}, verb...), "--dry-run"), rest...)...)
			assert.Equal(t, before, Snapshot(t, dryRoot), "the dry run changed the tree: %+v", dry)
			assert.Equal(t, real.Code, dry.Code, "exit: real %+v, dry %+v", real, dry)
			assert.Equal(t, statusWords(real), statusWords(dry), "status: real %+v, dry %+v", real, dry)
		})
	}
}

// statusWords is what a run's answer says: OK for a success, else the words of
// its first line that says FAIL, NO or REFUSED, up to and including that word
// (a NOTE line before it is commentary, not the answer).
func statusWords(r Result) string {
	if r.Code == 0 {
		return "OK"
	}
	for _, line := range strings.Split(r.Stderr+r.Stdout, "\n") {
		words := strings.Fields(line)
		for i, w := range words {
			w = strings.TrimRight(w, ":,")
			if w == "FAIL" || w == "NO" || w == "REFUSED" {
				return strings.Join(words[:i+1], " ")
			}
		}
	}
	return "no status word"
}
