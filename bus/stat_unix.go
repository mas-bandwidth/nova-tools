//go:build unix

package main

import (
	"os"
	"syscall"
)

// oNoFollow makes an open refuse a symlink as its last component.
const oNoFollow = syscall.O_NOFOLLOW

// statOf is a file's owner and, for a device node, the device it is (rdev).
func statOf(fi os.FileInfo) (uid uint32, rdev uint64, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return st.Uid, uint64(st.Rdev), true // Rdev is int32 on darwin, uint64 on linux
}
