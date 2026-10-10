package bench

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingCopy answers every shell line and fails the copy.
type failingCopy struct{ lines []string }

func (f *failingCopy) Shell(_ context.Context, _, line string, stdout, _ io.Writer) (int, error) {
	f.lines = append(f.lines, line)
	if line == MakeLine(DefaultRoot) {
		_, _ = io.WriteString(stdout, DefaultRoot+"/run.AbCd1234\n")
	}
	return 0, nil
}

func (f *failingCopy) Copy(context.Context, string, string, string, bool, io.Writer) error {
	return errors.New("tar on the bench exit 2")
}

// A copy that fails is an error, never a command status, and the directory the
// run made is still removed.
func TestRunRemovesTheRunDirectoryWhenTheCopyFails(t *testing.T) {
	t.Parallel()
	f := &failingCopy{}
	res, err := Run(context.Background(), f, Options{Hosts: []string{"vision"}, Dir: t.TempDir(), Argv: []string{"go", "version"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tar on the bench exit 2")
	assert.True(t, res.Removed)
	assert.Equal(t, []string{MakeLine(DefaultRoot), RemoveLine(DefaultRoot + "/run.AbCd1234")}, f.lines)
}

func TestLinesQuoteEveryWord(t *testing.T) {
	t.Parallel()
	assert.Equal(t, `cd '/r/run.x/repo' && mkdir -p ../tmp && TMPDIR='/r/run.x/tmp' GOTMPDIR='/r/run.x/tmp' GOCACHE='/c' GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 nice -n 19 'go' 'test' '-run' 'A B'\''C'`,
		ExecLine("/r/run.x", "/c", []string{"go", "test", "-run", "A B'C"}))
	assert.Contains(t, ExecLine("nova-bench/lanes/reader/run.a", DefaultCache, []string{"go", "test"}),
		`TMPDIR="$HOME"/'nova-bench/lanes/reader/run.a/tmp' GOTMPDIR="$HOME"/'nova-bench/lanes/reader/run.a/tmp' GOCACHE="$HOME"/'nova-bench/cache/go-build'`)
	assert.Equal(t, "rm -rf -- 'r/run.x'", RemoveLine("r/run.x"))
}

// The copy's tar stream holds the tree relative to its root, modes and
// symlinks kept, and the top .git only when asked for.
func TestWriteTreeLeavesGitOutUnlessAsked(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, ".git", "objects"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, ".git", "HEAD"), []byte("ref\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(src, "cmd", "x"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "cmd", "x", "main.go"), []byte("package main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "run.sh"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.Symlink("cmd/x/main.go", filepath.Join(src, "link")))

	read := func(withGit bool) map[string]*tar.Header {
		var b bytes.Buffer
		require.NoError(t, WriteTree(&b, src, withGit))
		got := map[string]*tar.Header{}
		tr := tar.NewReader(&b)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				return got
			}
			require.NoError(t, err)
			got[h.Name] = h
		}
	}
	got := read(false)
	assert.ElementsMatch(t, []string{"cmd/", "cmd/x/", "cmd/x/main.go", "link", "run.sh"}, slices.Collect(maps.Keys(got)))
	assert.Equal(t, int64(0o755), got["run.sh"].Mode&0o777)
	assert.Equal(t, "cmd/x/main.go", got["link"].Linkname)
	assert.Contains(t, read(true), ".git/HEAD")
}

func TestCopyLineUnpacksIntoTheRunDirectory(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "mkdir -p 'r/run.x/repo' && tar -C 'r/run.x/repo' -xf -", CopyLine("r/run.x/repo"))
}
