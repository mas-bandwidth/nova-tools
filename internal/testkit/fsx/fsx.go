// Package fsx sets up the files a test needs in one line, instead of the
// os.MkdirAll, os.WriteFile and require.NoError block repeated across the
// suite. It is a thin front door over pkg/testkit's file helpers: Write wraps
// testkit.WriteFile, Read wraps testkit.ReadFile, and Tree delegates the plain
// files to testkit.Tree and adds the modes and a t.TempDir of its own.
//
// A tree is a t.TempDir with every key written under it, parents made:
//
//	dir := fsx.Tree(t, map[string]string{
//		"conf/app.toml": "k = 1\n",
//		"bin/run.sh":    fsx.Exec("#!/bin/sh\nexit 0\n"),
//	})
//
// A key is a slash-separated path inside that temp dir, never absolute and
// never climbing out with "..": such a key fails the test, naming it. A body
// wrapped in Exec is written with mode 0o755, every other with 0o644. Read,
// Exists and Lines answer about a path; Write puts a file at one, making its
// parents. Each takes testing.TB first, calls t.Helper and fails through
// testify's require, so one bad row does not hide the rest of a table.
package fsx

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/require"
)

// execMarker prefixes an Exec body so Tree writes it executable. It is a NUL
// (impossible in a Go source string a caller writes by hand) so no ordinary
// body collides with it; Exec is the only thing that makes one.
const execMarker = "\x00fsx-exec\x00"

// Exec marks body to be written with mode 0o755 by Tree, so a test's script
// runs:
//
//	"run.sh": fsx.Exec("#!/bin/sh\nexit 0\n")
func Exec(body string) string { return execMarker + body }

// Tree makes a fresh t.TempDir, writes each file under it with its parents,
// and returns the directory. A key is a slash-separated path inside the
// directory: an absolute key, or one that climbs out with "..", is refused
// before anything is written, failing the test and naming the key. Every file
// is 0o644 except a body wrapped in Exec, which is 0o755.
func Tree(t testing.TB, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	plain := make(map[string]string, len(files))
	exec := make(map[string]string, len(files))
	for name, body := range files {
		require.True(t, filepath.IsLocal(filepath.FromSlash(name)),
			"fsx.Tree: %q escapes the temp dir %s; a key is a relative path inside it", name, dir)
		if b, ok := strings.CutPrefix(body, execMarker); ok {
			exec[name] = b
			continue
		}
		plain[name] = body
	}
	testkit.Tree(t, dir, plain)
	for name, body := range exec {
		path := filepath.Join(dir, filepath.FromSlash(name))
		testkit.WriteFile(t, path, body)
		require.NoError(t, os.Chmod(path, 0o755), "fsx.Tree: making %q executable", name)
	}
	return dir
}

// Mkdir makes path and its parents, failing the test when it cannot. It is the
// os.MkdirAll and require.NoError block as one call, for the directories a
// tree of files does not name.
func Mkdir(t testing.TB, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(path, 0o755))
}

// Write writes body to path with mode 0o644, making the parent directories
// first, and fails the test on any error.
func Write(t testing.TB, path, body string) {
	t.Helper()
	testkit.WriteFile(t, path, body)
}

// Read returns the file at path, failing the test when it cannot be read.
func Read(t testing.TB, path string) string {
	t.Helper()
	return testkit.ReadFile(t, path)
}

// Exists reports whether path is there. A path that is absent is false; any
// other stat error fails the test, so a permission problem is never mistaken
// for an absent file.
func Exists(t testing.TB, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	require.ErrorIs(t, err, fs.ErrNotExist, "fsx.Exists: stat %s", path)
	return false
}

// Lines returns the file at path split into lines, a trailing newline dropped
// and an empty file no lines at all, so a listing's shape is one call.
func Lines(t testing.TB, path string) []string {
	t.Helper()
	body := testkit.ReadFile(t, path)
	if body == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(body, "\n"), "\n")
}
