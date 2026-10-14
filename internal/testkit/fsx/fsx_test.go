package fsx_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit/fsx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTreeCreatesNestedFilesAndReturnsTempDir(t *testing.T) {
	t.Parallel()
	dir := fsx.Tree(t, map[string]string{
		"a/b.txt": "content",
		"c.sh":    fsx.Exec("#!/bin/sh\necho hi\n"),
		"deep/nested/file.go": "package main\n",
	})
	require.NotEmpty(t, dir)
	assert.True(t, filepath.IsAbs(dir))

	content, err := os.ReadFile(filepath.Join(dir, "a", "b.txt"))
	require.NoError(t, err)
	assert.Equal(t, "content", string(content))

	scriptContent, err := os.ReadFile(filepath.Join(dir, "c.sh"))
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\necho hi\n", string(scriptContent))

	info, err := os.Stat(filepath.Join(dir, "c.sh"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())

	deepContent, err := os.ReadFile(filepath.Join(dir, "deep", "nested", "file.go"))
	require.NoError(t, err)
	assert.Equal(t, "package main\n", string(deepContent))
}

func TestTreeRefusesEscapingPaths(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"../out.txt", "a/../../out.txt", "/abs.txt", ""} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{TB: t}
			runs(rec, func() { fsx.Tree(rec, map[string]string{"fine.txt": "x", name: "y"}) })
			assert.True(t, rec.failed, "Tree wrote %q", name)
			assert.Contains(t, rec.msg, "escapes the root")
		})
	}
}

func TestTreeRefusesAbsoluteKey(t *testing.T) {
	t.Parallel()
	rec := &recorder{TB: t}
	runs(rec, func() { fsx.Tree(rec, map[string]string{"/absolute/path.txt": "x"}) })
	assert.True(t, rec.failed)
	assert.Contains(t, rec.msg, "escapes the root")
}

func TestTreeWithEmptyBody(t *testing.T) {
	t.Parallel()
	dir := fsx.Tree(t, map[string]string{"empty.txt": ""})
	content, err := os.ReadFile(filepath.Join(dir, "empty.txt"))
	require.NoError(t, err)
	assert.Empty(t, string(content))
}

func TestWriteReadExistsLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")

	fsx.Write(t, filePath, "hello\nworld\n")
	assert.Equal(t, "hello\nworld\n", fsx.Read(t, filePath))
	assert.True(t, fsx.Exists(t, filePath))
	assert.Equal(t, []string{"hello", "world"}, fsx.Lines(t, filePath))

	assert.False(t, fsx.Exists(t, filepath.Join(dir, "missing.txt")))
}

func TestWriteWithExecMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "script.sh")

	fsx.Write(t, filePath, fsx.Exec("#!/bin/sh\necho hi\n"))

	info, err := os.Stat(filePath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\necho hi\n", string(content))
}

func TestLinesHandlesTrailingNewline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "lines.txt")

	fsx.Write(t, filePath, "line1\nline2\n")
	assert.Equal(t, []string{"line1", "line2"}, fsx.Lines(t, filePath))

	fsx.Write(t, filePath, "line1\nline2")
	assert.Equal(t, []string{"line1", "line2"}, fsx.Lines(t, filePath))

	fsx.Write(t, filePath, "")
	assert.Nil(t, fsx.Lines(t, filePath))

	fsx.Write(t, filePath, "\n\n")
	assert.Equal(t, []string{"", ""}, fsx.Lines(t, filePath))
}

func TestReadFailsOnMissingFile(t *testing.T) {
	t.Parallel()
	rec := &recorder{TB: t}
	runs(rec, func() { fsx.Read(rec, "/nonexistent/path.txt") })
	assert.True(t, rec.failed)
}

func TestExistsFailsOnPermissionError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	restricted := filepath.Join(dir, "restricted")
	require.NoError(t, os.MkdirAll(restricted, 0o000))
	defer os.Chmod(restricted, 0o755)

	rec := &recorder{TB: t}
	runs(rec, func() { fsx.Exists(rec, filepath.Join(restricted, "file.txt")) })
	assert.True(t, rec.failed)
}

func TestWriteCreatesParents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "a", "b", "c.txt")

	fsx.Write(t, filePath, "content")

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, "content", string(content))
}

func TestTreeOverwritesWithWrite(t *testing.T) {
	t.Parallel()
	dir := fsx.Tree(t, map[string]string{"file.txt": "first"})
	fsx.Write(t, filepath.Join(dir, "file.txt"), "second")

	content, err := os.ReadFile(filepath.Join(dir, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "second", string(content))
}

type recorder struct {
	testing.TB
	failed, skipped bool
	msg             string
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.failed = true
	r.msg += fmt.Sprintf(format, args...)
}
func (r *recorder) FailNow() { panic(r) }
func (r *recorder) Skipf(format string, args ...any) {
	r.skipped = true
	r.msg += fmt.Sprintf(format, args...)
	panic(r)
}

func runs(r *recorder, f func()) {
	defer func() {
		if p := recover(); p != nil && p != any(r) {
			panic(p)
		}
	}()
	f()
}
