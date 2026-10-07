//go:build !windows

package main

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// liveInode is path's inode read here with os.Stat and syscall, not with the
// inodeOf the code under test uses, so the expectation is independent of it.
func liveInode(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Stat(path)
	require.NoError(t, err)
	st, ok := fi.Sys().(*syscall.Stat_t)
	require.True(t, ok, "no stat_t for %s", path)
	return uint64(st.Ino)
}
