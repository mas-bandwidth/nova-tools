//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// A character device alone is not a terminal (/dev/null is one too).
func shellTerminal(f *os.File) bool {
	var size [4]uint16
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&size)))
	return err == 0
}
