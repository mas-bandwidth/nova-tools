package readregular

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultMax(t *testing.T) {
	t.Parallel()
	assert.Equal(t, int64(16<<20), DefaultMax)
}

func TestReadRegularFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := []byte("hello regular file")
	require.NoError(t, os.WriteFile(path, content, 0o644))

	got, err := Read(path, DefaultMax)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

func TestReadSymlinkToRegularFile(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlinks on windows require special privileges")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	content := []byte("through symlink")
	require.NoError(t, os.WriteFile(target, content, 0o644))

	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	got, err := Read(link, DefaultMax)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

func TestReadNonRegularFileDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	require.NoError(t, os.Mkdir(subdir, 0o755))

	_, err := Read(subdir, DefaultMax)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "not a regular file"), "got error %v, want 'not a regular file'", err)
}

func TestReadNonRegularFileSocket(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("unix domain sockets on windows not guaranteed")
	}

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Skipf("unix socket unavailable: %v", err)
	}
	defer func() { _ = l.Close() }() // ignored: the test listener is done with

	_, err = Read(sockPath, DefaultMax)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "not a regular file"), "got error %v, want 'not a regular file'", err)
}

func TestReadFileExceedsLimitAtStat(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "large.txt")
	require.NoError(t, os.WriteFile(path, []byte("0123456789"), 0o644))

	_, err := Read(path, 5)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "exceeds limit 5"), "got error %v, want naming cap", err)
}

func TestReadFileExactLimitPasses(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "exact.txt")
	content := []byte("0123456789")
	require.NoError(t, os.WriteFile(path, content, 0o644))

	got, err := Read(path, 10)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

func TestReadNonExistentFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "missing.txt")

	_, err := Read(path, DefaultMax)
	require.Error(t, err)
	assert.True(t, os.IsNotExist(err), "got error %v, want os.IsNotExist", err)
}
