package testkit

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Tree writes each file of files under root, making the directories, and
// returns root. A name is a slash-separated path inside root; a name that is
// absolute or climbs out of root with ".." fails the test before anything is
// written, so a fixture never writes outside the test's own directory.
//
//	root := testkit.Tree(t, t.TempDir(), map[string]string{"tla/CASES.tsv": plan, "go.mod": "module x\n"})
func Tree(t testing.TB, root string, files map[string]string) string {
	t.Helper()
	for name := range files {
		require.True(t, filepath.IsLocal(filepath.FromSlash(name)), "tree: %q escapes the root %s; a fixture name is a relative path inside it", name, root)
	}
	for name, body := range files {
		WriteFile(t, filepath.Join(root, filepath.FromSlash(name)), body)
	}
	return root
}

// CopyTree copies the tree at src into dst, making dst and its directories and
// overwriting files already there; each file keeps its permission bits, so a
// fixture's script stays executable. A .git directory is not copied, and
// neither is anything that is not a regular file or a directory (a link is
// never followed out of the tree). It fails the test, naming the path, on any
// error.
func CopyTree(t testing.TB, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir() && d.Name() == ".git":
			return fs.SkipDir
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case !d.Type().IsRegular():
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, info.Mode().Perm())
	})
	require.NoError(t, err, "copy tree %s to %s", src, dst)
}

// JSON decodes raw (a tool's --json output, a stored record) into a T,
// failing the test with the text when it does not decode.
func JSON[T any](t testing.TB, raw string) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal([]byte(raw), &v), "decode JSON %q", raw)
	return v
}
