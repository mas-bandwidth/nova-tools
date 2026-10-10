//go:build !windows

package main

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWakeCursorRefusesNonseekablePipeBeforeOpen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pipe")
	require.NoError(t, syscall.Mkfifo(path, 0o600))
	_, err := realWakeArm(path, "")
	require.ErrorContains(t, err, "seekable regular file")
}

func TestWakeCursorRefusesPipeReplacementAtOpen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "wake")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	pipe := path + ".pipe"
	require.NoError(t, syscall.Mkfifo(pipe, 0o600))
	f, _, err := wakeFileWithOpen(path, wakeCursor{}, func(path string) (*os.File, error) {
		require.NoError(t, os.Rename(pipe, path))
		return openSafeWake(path)
	})
	require.ErrorContains(t, err, "seekable regular file after open")
	require.Nil(t, f)
}
