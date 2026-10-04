//go:build unix

package filelock

import (
	"os"
	"path/filepath"
	"strings"
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
	require.NoError(t, err, err)
	defer f.Close()

	// Normal match
	match, err := verifyInode(f, path)
	require.True(t, err == nil && match, "verifyInode normal = %v, %v, want true, nil", match, err)

	// File removed from disk
	require.NoError(t, os.Remove(path))
	matchRemoved, err := verifyInode(f, path)
	assert.True(t, err == nil && !matchRemoved, "verifyInode removed = %v, %v, want false, nil", matchRemoved, err)

	// Recreated different file at path
	require.NoError(t, os.WriteFile(path, []byte("new"), 0666))
	matchRecreated, err := verifyInode(f, path)
	assert.True(t, err == nil && !matchRecreated, "verifyInode recreated = %v, %v, want false, nil", matchRecreated, err)

	// Pass directory as fd
	dirF, err := os.Open(dir)
	require.NoError(t, err, err)
	defer dirF.Close()
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

	// openFileSafe with O_RDONLY must not block on FIFO and must refuse it as not a regular file
	_, err := openFileSafe(fifoPath, os.O_RDONLY, 0)
	require.Error(t, err, "openFileSafe on FIFO succeeded, want error")

	// ReadStamp on FIFO must also not block and return error
	_, err = ReadStamp(fifoPath)
	require.Error(t, err, "ReadStamp on FIFO succeeded, want error")
}

func TestMutant_VerifyInode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "verify_mutant.lock")

	calls := 0
	opts := options{
		verifyInode: func(f *os.File, path string) (bool, error) {
			calls++
			return false, nil // always mismatch
		},
	}

	lock, err := tryLockWithOptions(path, "mismatch", opts)
	lock.Unlock() // nil-safe: releases a mutant lock, no-ops on nil
	require.Nil(t, lock, "tryLockWithOptions succeeded despite inode mismatch (mutant: verifyInode check bypassed)")
	require.True(t, err != nil && strings.Contains(err.Error(), "failed after 5 inode collision retries"), "err = %v, want 'failed after 5 inode collision retries'", err)
	require.Equal(t, 5, calls, "verifyInode called %d times, want 5 retries", calls)
}
