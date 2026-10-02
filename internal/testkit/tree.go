package testkit

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Tree writes each file of files under root, making root and the directories,
// overwriting a file already there, and returns root. A name is a
// slash-separated path inside root. Every name is checked before anything is
// written (filepath.IsLocal: not absolute, never climbing out with ".."), and
// every write goes through an os.Root opened on root, which refuses to follow
// a symlink out of it; either refusal fails the test naming the path.
//
//	root := testkit.Tree(t, t.TempDir(), map[string]string{"tla/CASES.tsv": plan, "go.mod": "module x\n"})
//
// A copy of a fixture directory needs no helper: require.NoError(t, os.CopyFS(dst, os.DirFS(src))).
func Tree(t testing.TB, root string, files map[string]string) string {
	t.Helper()
	for name := range files {
		require.True(t, filepath.IsLocal(filepath.FromSlash(name)), "tree: %q escapes the root %s; a fixture name is a relative path inside it", name, root)
	}
	require.NoError(t, os.MkdirAll(root, 0o755))
	r, err := os.OpenRoot(root)
	require.NoError(t, err)
	defer func() {
		// ignored: a deferred close on the failure path; the write that failed the test is the error named
		_ = r.Close()
	}()
	for name, body := range files {
		require.NoError(t, r.MkdirAll(path.Dir(name), 0o755), "tree: %q under %s", name, root)
		require.NoError(t, r.WriteFile(name, []byte(body), 0o644), "tree: %q under %s", name, root)
	}
	require.NoError(t, r.Close(), "tree: closing %s", root)
	return root
}

// JSON decodes raw (a tool's --json output, a stored record) into a T, the
// three lines of a declared value, json.Unmarshal and its require as one
// expression, failing the test with the text when it does not decode.
func JSON[T any](t testing.TB, raw string) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal([]byte(raw), &v), "decode JSON %q", raw)
	return v
}
