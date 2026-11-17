//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func odd(path string) string {
	return path + " is not a regular file; nova-reserve opens no link or special file in its directory; run: sudo rm -f " + shellQuote(path)
}

func linked(path string) string {
	return path + " has 2 names (a hard link); nova-reserve opens no file in its directory that has another name; run: sudo rm -f " + shellQuote(path)
}

func private(t *testing.T) (path string, same func()) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "private")
	require.NoError(t, os.WriteFile(path, []byte("export PATH=/usr/bin\n"), 0o600))
	return path, func() {
		t.Helper()
		b, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "export PATH=/usr/bin\n", string(b))
		fi, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	}
}

func requireOdd(t *testing.T, err error, path string) {
	t.Helper()
	var e *exitError
	require.ErrorAs(t, err, &e)
	assert.Equal(t, exitError{2, odd(path)}, *e)
}

func hardLink(t *testing.T, path string) func() {
	t.Helper()
	target := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(target, []byte(`{"usable_cores":4,"resources":{"cpu":4,"memory":8}}`+"\n"), 0o600))
	require.NoError(t, os.Link(target, path))
	return func() {
		t.Helper()
		fi, err := os.Stat(target)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	}
}

func TestCapacityThroughALinkIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target, same := private(t)
	link := capPath(h.dir)
	require.NoError(t, os.Symlink(target, link))

	assert.Equal(t, 2, run([]string{"--dir", h.dir, "--", "make"}, h.w))
	assert.Equal(t, "nova-reserve: "+odd(link)+"\n", h.err.String())
	h.err.Reset()
	assert.Equal(t, 2, run([]string{"set", "--dir", h.dir, "--cpu", "4", "--memory", "8"}, h.w))
	assert.Equal(t, "nova-reserve set: "+odd(link)+"\n", h.err.String())
	h.err.Reset()
	assert.Equal(t, 2, run([]string{"status", "--dir", h.dir}, h.w))
	assert.Equal(t, "nova-reserve status: "+odd(link)+"\n", h.err.String())
	same()
}

func TestTempCapacityThroughALinkIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := filepath.Join(t.TempDir(), "made")
	link := capPath(h.dir) + ".42.tmp"
	require.NoError(t, os.Symlink(target, link))
	assert.Equal(t, 2, run([]string{"set", "--dir", h.dir, "--cpu", "4", "--memory", "8"}, h.w))
	assert.Equal(t, "nova-reserve set: "+odd(link)+"\n", h.err.String())
	assert.NoFileExists(t, target)
}

func TestLockAndDirectoryAndFIFOThroughSpecialFilesAreRefused(t *testing.T) {
	t.Parallel()
	target, same := private(t)
	link := filepath.Join(t.TempDir(), "serve.lock")
	require.NoError(t, os.Symlink(target, link))
	_, err := openIn(link, os.O_RDWR|os.O_CREATE)
	requireOdd(t, err, link)
	same()

	dirLock := filepath.Join(t.TempDir(), "serve.lock")
	require.NoError(t, os.Mkdir(dirLock, 0o755))
	_, err = openIn(dirLock, os.O_RDWR|os.O_CREATE)
	requireOdd(t, err, dirLock)

	h := newHarness(t)
	fifo := capPath(h.dir)
	require.NoError(t, syscall.Mkfifo(fifo, 0o666))
	_, _, err = readCapacity(h.dir)
	requireOdd(t, err, fifo)
}

func TestHardLinkedCapacityAndLockAreRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := capPath(h.dir)
	same := hardLink(t, p)
	assert.Equal(t, 2, run([]string{"set", "--dir", h.dir, "--cpu", "4", "--memory", "8"}, h.w))
	assert.Equal(t, "nova-reserve set: "+linked(p)+"\n", h.err.String())
	h.err.Reset()
	assert.Equal(t, 2, run([]string{"--dir", h.dir, "--", "make"}, h.w))
	assert.Equal(t, "nova-reserve: "+linked(p)+"\n", h.err.String())
	same()

	lp := filepath.Join(t.TempDir(), "serve.lock")
	sameLock := hardLink(t, lp)
	_, err := openIn(lp, os.O_RDWR|os.O_CREATE)
	var e *exitError
	require.ErrorAs(t, err, &e)
	assert.Equal(t, exitError{2, linked(lp)}, *e)
	sameLock()
}

func TestDirectoryLinkIsRefused(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	require.NoError(t, os.Chmod(target, 0o700))
	link := filepath.Join(t.TempDir(), "reserve")
	require.NoError(t, os.Symlink(target, link))
	h := newHarness(t)
	assert.Equal(t, 2, run([]string{"--dir", link, "--", "make"}, h.w))
	assert.Equal(t, "nova-reserve: --dir "+link+" is a symbolic link; nova-reserve uses no link as its directory: give the directory it names, or remove the link; run: ls -ld "+shellQuote(link)+"\n", h.err.String())
}

// Not parallel: mutates process-wide umask to verify multi-user 0666 file creation.
func TestMadeFilesAreForEveryUser(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	h := newHarness(t)
	h.setCap(t, 2, 4, 2)
	fi, err := os.Stat(filepath.Join(h.dir, "capacity.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o666), fi.Mode().Perm())
}
