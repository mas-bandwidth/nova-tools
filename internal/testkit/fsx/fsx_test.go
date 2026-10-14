package fsx

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recording is a testing.TB that records every failure instead of ending the
// test, so a failing row is observed, not assumed. TempDir hands back a
// directory the caller named, so a row can inspect what Tree wrote. It embeds
// the interface for the methods that stay unused here; every path that matters
// is overridden.
type recording struct {
	testing.TB
	dir     string
	failed  bool
	message []string
}

func (r *recording) Helper()         {}
func (r *recording) TempDir() string { return r.dir }
func (r *recording) Cleanup(func())  {}
func (r *recording) Fail()           { r.failed = true }
func (r *recording) FailNow()        { r.failed = true }
func (r *recording) Failed() bool    { return r.failed }
func (r *recording) Error(args ...any) {
	r.failed = true
	r.message = append(r.message, fmt.Sprint(args...))
}
func (r *recording) Errorf(format string, args ...any) {
	r.failed = true
	r.message = append(r.message, fmt.Sprintf(format, args...))
}
func (r *recording) Fatal(args ...any) {
	r.failed = true
	r.message = append(r.message, fmt.Sprint(args...))
}
func (r *recording) Fatalf(format string, args ...any) {
	r.failed = true
	r.message = append(r.message, fmt.Sprintf(format, args...))
}

// TestFsxTreeMakesFilesAndRefusesEscapes pins Tree on its rows: a sound tree
// makes every file with its parents and its mode, and a key that is absolute or
// climbs out is refused before anything is written, observed through the
// recording TB rather than ending the run.
func TestFsxTreeMakesFilesAndRefusesEscapes(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name    string
		files   map[string]string
		failing bool
	}{
		{"nested", map[string]string{"a/b.txt": "x", "top": "y"}, false},
		{"script-mode", map[string]string{"bin/run.sh": Exec("#!/bin/sh\n")}, false},
		{"empty-body", map[string]string{"empty": ""}, false},
		{"absolute-key", map[string]string{"/etc/hosts": "x"}, true},
		{"climbing-key", map[string]string{"../out.txt": "x"}, true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			rec := &recording{TB: t, dir: dir}
			Tree(rec, row.files)
			if row.failing {
				assert.True(t, rec.failed, "an escaping key passed: %v", row.files)
				return
			}
			require.False(t, rec.failed, "a sound tree failed: %v", rec.message)
			if body, ok := row.files["a/b.txt"]; ok {
				assert.Equal(t, body, Read(t, filepath.Join(dir, "a", "b.txt")), "a nested file is not where the key names it")
			}
			if _, ok := row.files["bin/run.sh"]; ok {
				info, err := os.Stat(filepath.Join(dir, "bin", "run.sh"))
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(0o755), info.Mode().Perm(), "an Exec file is not executable")
				assert.Equal(t, "#!/bin/sh\n", Read(t, filepath.Join(dir, "bin", "run.sh")))
			}
			if _, ok := row.files["empty"]; ok {
				assert.True(t, Exists(t, filepath.Join(dir, "empty")))
				assert.Equal(t, "", Read(t, filepath.Join(dir, "empty")))
			}
		})
	}
}

// TestFsxWriteReadExistsLines pins the four path helpers: Write makes the
// parents, Read returns the body, Exists is true for what was written and false
// for what was not, and Lines drops a trailing newline and answers no lines for
// an empty file.
func TestFsxWriteReadExistsLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rows := []struct {
		name  string
		rel   string
		body  string
		lines []string
	}{
		{"nested", "n/listing.txt", "one\ntwo\n", []string{"one", "two"}},
		{"no-trailing-newline", "plain", "solo", []string{"solo"}},
		{"empty", "empty", "", nil},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(dir, filepath.FromSlash(row.rel))
			Write(t, path, row.body)
			assert.True(t, Exists(t, path), "a file just written is not there")
			assert.Equal(t, row.body, Read(t, path))
			assert.Equal(t, row.lines, Lines(t, path))
		})
	}
	assert.False(t, Exists(t, filepath.Join(dir, "absent")), "an absent file reports present")
}

// ExampleTree is the package doc's call, run by the go tool: a sound tree is
// made and the nested file is there, so the recording TB records nothing.
func ExampleTree() {
	dir, err := os.MkdirTemp("", "fsx-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	rec := &recording{dir: dir}
	Tree(rec, map[string]string{"a/b.txt": "x", "bin/run.sh": Exec("#!/bin/sh\n")})
	fmt.Println(rec.failed, Exists(rec, filepath.Join(dir, "a", "b.txt")))
	// Output: false true
}
