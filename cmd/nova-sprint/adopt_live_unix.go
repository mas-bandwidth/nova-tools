//go:build !windows

package main

import (
	"os"
	"syscall"
)

// inodeOf is the inode of path, 0 when it cannot be read: the adopt play tells a
// binary replaced on a fresh inode from one overwritten in place.
func inodeOf(path string) uint64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}
