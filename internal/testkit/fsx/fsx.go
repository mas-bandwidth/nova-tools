package fsx

import (
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Package fsx provides one-line file setup for tests. It creates a temporary
// directory, writes files with slash-separated paths, and handles executable
// modes.
//
// Example:
//
//	dir := fsx.Tree(t, map[string]string{
//	    "config.yaml": "port: 8080\n",
//	    "bin/run.sh":  fsx.Exec("#!/bin/sh\necho hello\n"),
//	})
//	fsx.Write(t, filepath.Join(dir, "extra.txt"), "added later")
//	content := fsx.Read(t, filepath.Join(dir, "config.yaml"))
//	lines := fsx.Lines(t, filepath.Join(dir, "bin/run.sh"))
//	fsx.Exists(t, filepath.Join(dir, "config.yaml"))
func Example() {
	// This example is a no-op; see the package comment for usage.
}

// Tree creates a temporary directory, writes the given files into it, and returns
// the directory path. Keys are slash-separated paths relative to the temp dir;
// parents are created automatically. A key that is absolute or contains ".." fails
// the test. Each file is written with mode 0o644 unless marked with Exec.
//
//	dir := fsx.Tree(t, map[string]string{
//	    "a/b.txt": "content",
//	    "script.sh": fsx.Exec("#!/bin/sh\necho hi\n"),
//	})
func Tree(t testing.TB, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name := range files {
		require.True(t, filepath.IsLocal(filepath.FromSlash(name)), "fsx: %q escapes the root", name)
	}
	require.NoError(t, os.MkdirAll(root, 0o755))
	r, err := os.OpenRoot(root)
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	for name, body := range files {
		mode := fsModeFromBody(body)
		require.NoError(t, r.MkdirAll(path.Dir(name), 0o755), "fsx: %q under %s", name, root)
		require.NoError(t, r.WriteFile(name, []byte(execBody(body)), mode), "fsx: %q under %s", name, root)
	}
	require.NoError(t, r.Close(), "fsx: closing %s", root)
	return root
}

// Exec marks a file body as executable (mode 0o755).
func Exec(body string) string {
	return "\x00EXEC\x00" + body
}

// isExec checks if a body is marked as executable.
func isExec(body string) bool {
	return len(body) > 6 && body[:6] == "\x00EXEC\x00"
}

// execBody returns the actual body without the Exec marker.
func execBody(body string) string {
	if isExec(body) {
		return body[6:]
	}
	return body
}

// fsModeFromBody returns 0o755 if the body is marked with Exec, else 0o644.
func fsModeFromBody(body string) os.FileMode {
	if isExec(body) {
		return 0o755
	}
	return 0o644
}

// Write writes body to the file at path (relative to the test's temp dir or an
// absolute path), creating parents as needed. Fails the test on error.
func Write(t testing.TB, filePath, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filePath), 0o755))
	mode := fsModeFromBody(body)
	require.NoError(t, os.WriteFile(filePath, []byte(execBody(body)), mode))
}

// Read returns the contents of the file at path. Fails the test on error.
func Read(t testing.TB, filePath string) string {
	t.Helper()
	raw, err := os.ReadFile(filePath)
	require.NoError(t, err)
	return string(raw)
}

// Exists checks whether the file at path exists. Fails the test on error other
// than not-exist. Returns true if the file exists, false otherwise.
func Exists(t testing.TB, filePath string) bool {
	t.Helper()
	_, err := os.Stat(filePath)
	if err == nil {
		return true
	}
	require.True(t, os.IsNotExist(err), "fsx: stat %s: %v", filePath, err)
	return false
}

// Lines reads the file at path and returns its lines (split on \n, trailing
// empty line dropped). Fails the test on error.
func Lines(t testing.TB, filePath string) []string {
	t.Helper()
	content := Read(t, filePath)
	if content == "" {
		return nil
	}
	return splitLines(content)
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	// Always append the final part (may be empty if string ends with \n)
	lines = append(lines, s[start:])
	// Drop the trailing empty line if the string ended with \n
	if len(lines) > 0 && lines[len(lines)-1] == "" && len(s) > 0 && s[len(s)-1] == '\n' {
		lines = lines[:len(lines)-1]
	}
	return lines
}
