//go:build !windows

package main

import (
	"github.com/stretchr/testify/require"
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
