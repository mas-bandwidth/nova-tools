//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// wakeIdentity is the filesystem identity os.SameFile compares, saved across
// process restart (SPEC-BUS, wait; tla/WakeCursor.tla).
func wakeIdentity(_ *os.File, fi os.FileInfo) (string, error) {
	st := fi.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino), nil
}

// openSafeWake cannot block if a regular path is replaced by a FIFO before
// open. The handle's type and identity are checked before any read.
func openSafeWake(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
