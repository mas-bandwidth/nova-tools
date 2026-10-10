//go:build unix

package bench

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBenchExecCoverWriteTreeSrcDoesNotExist(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), "nonexistent")
	var b bytes.Buffer
	err := WriteTree(&b, src, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such file or directory")
}

func TestBenchExecCoverWriteTreeTopGitIsFile(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, ".git"), []byte("ref: HEAD\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(src, "cmd"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "cmd", "main.go"), []byte("package main\n"), 0o644))

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
	gotWithout := read(false)
	_, hasGit := gotWithout[".git"]
	assert.False(t, hasGit)
	gotWith := read(true)
	_, hasGit = gotWith[".git"]
	assert.True(t, hasGit)
}

func TestBenchExecCoverWriteTreeNestedGitDirKept(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "dir", ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "dir", ".git", "HEAD"), []byte("ref: HEAD\n"), 0o644))

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
	gotWithout := read(false)
	assert.Contains(t, gotWithout, "dir/.git/")
	gotWith := read(true)
	assert.Contains(t, gotWith, "dir/.git/")
}

func TestBenchExecCoverWriteTreeFIFORefused(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	fifo := filepath.Join(src, "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(src, "cmd"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "cmd", "main.go"), []byte("package main\n"), 0o644))

	var b bytes.Buffer
	err := WriteTree(&b, src, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fifo")
	assert.Contains(t, err.Error(), "refusing to copy it")
}

func TestBenchExecCoverWriteTreeEmptyDir(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	emptyDir := filepath.Join(src, "empty")
	require.NoError(t, os.Mkdir(emptyDir, 0o755))

	var b bytes.Buffer
	require.NoError(t, WriteTree(&b, src, false))

	tr := tar.NewReader(&b)
	var found *tar.Header
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if h.Name == "empty/" {
			found = h
		}
	}
	require.NotNil(t, found)
	assert.Equal(t, "empty/", found.Name)
	assert.Equal(t, int64(0o755), found.Mode&0o777)
}
