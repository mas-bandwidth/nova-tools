//go:build !windows

package main

import (
	"os"
	"syscall"
)

// fileOwner is the uid that owns fi, and whether the platform says.
func fileOwner(fi os.FileInfo) (int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}
