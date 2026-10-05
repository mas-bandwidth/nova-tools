//go:build unix

package filelock

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyInode_EdgeCases(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "verify.lock")

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0666)
	require.NoError(t, err)
	defer func() { _ = f.Close() }() // ignored: the test writes nothing through it, a close error loses nothing

	match, err := verifyInode(f, path)
	require.True(t, err == nil && match, "verifyInode normal = %v, %v, want true, nil", match, err)

	require.NoError(t, os.Remove(path))
	matchRemoved, err := verifyInode(f, path)
	assert.True(t, err == nil && !matchRemoved, "verifyInode removed = %v, %v, want false, nil", matchRemoved, err)

	require.NoError(t, os.WriteFile(path, []byte("new"), 0666))
	matchRecreated, err := verifyInode(f, path)
	assert.True(t, err == nil && !matchRecreated, "verifyInode recreated = %v, %v, want false, nil", matchRecreated, err)

	dirF, err := os.Open(dir)
	require.NoError(t, err)
	defer func() { _ = dirF.Close() }() // ignored: the open is read-only, a close error loses nothing
	_, errDir := verifyInode(dirF, dir)
	assert.Error(t, errDir, "verifyInode on directory should error")
}

func TestOpenFileSafe_FIFO(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fifoPath := filepath.Join(dir, "test.fifo")
	if err := syscall.Mkfifo(fifoPath, 0666); err != nil {
		t.Skipf("mkfifo not supported: %v", err)
	}

	// openFileSafe must not block on a FIFO and must refuse it as not a regular file.
	_, err := openFileSafe(fifoPath, os.O_RDONLY, 0)
	require.Error(t, err, "openFileSafe on FIFO succeeded, want error")

	// ReadStamp must not block on a FIFO either.
	_, err = ReadStamp(fifoPath)
	require.Error(t, err, "ReadStamp on FIFO succeeded, want error")
}

func TestMutant_VerifyInode(t *testing.T) {
	t.Parallel()

	path := newRig(t).path("verify_mutant.lock")
	calls := 0
	lock, err := tryLockWithOptions(path, "mismatch", options{
		verifyInode: func(f *os.File, path string) (bool, error) {
			calls++
			return false, nil // always mismatch
		},
	})
	release(t, lock) // nil-safe: releases a mutant lock, no-ops on nil
	require.Nil(t, lock, "tryLockWithOptions succeeded despite inode mismatch (mutant: verifyInode check bypassed)")
	require.ErrorContains(t, err, "failed after 5 inode collision retries")
	require.Equal(t, 5, calls, "verifyInode called %d times, want 5 retries", calls)
}
