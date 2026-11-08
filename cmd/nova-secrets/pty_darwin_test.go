//go:build functional

package main

import (
	"bytes"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPty opens a new pseudo-terminal pair: grant, unlock, then the slave by the name the master reports.
func openPty() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	fd := master.Fd()
	name := make([]byte, 128)
	for _, c := range []struct{ req, arg uintptr }{{unix.TIOCPTYGRANT, 0}, {unix.TIOCPTYUNLK, 0}, {unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))}} {
		if _, _, e := unix.Syscall(unix.SYS_IOCTL, fd, c.req, c.arg); e != 0 {
			_ = master.Close() // ignored: the ioctl error is the one returned
			return nil, nil, e
		}
	}
	slave, err = os.OpenFile(string(name[:bytes.IndexByte(name, 0)]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close() // ignored: the open error is the one returned
		return nil, nil, err
	}
	return master, slave, nil
}
