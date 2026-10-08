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
