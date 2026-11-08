//go:build functional

package main

import (
	"bytes"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPty opens a pseudo-terminal: the master, and the path of its slave.
func openPty() (*os.File, string, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, "", err
	}
	fd := int(m.Fd())
	var name [128]byte
	err = unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0)
	if err == nil {
		err = unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0)
	}
	if err == nil {
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))); e != 0 {
			err = e
		}
	}
	if err != nil {
		_ = m.Close()
		return nil, "", err
	}
	return m, string(name[:bytes.IndexByte(name[:], 0)]), nil
}
