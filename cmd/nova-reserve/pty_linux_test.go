//go:build functional

package main

import (
	"os"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// openPty opens a pseudo-terminal: the master, and the path of its slave.
func openPty() (*os.File, string, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, "", err
	}
	fd := int(m.Fd())
	err = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0)
	n := 0
	if err == nil {
		n, err = unix.IoctlGetInt(fd, unix.TIOCGPTN)
	}
	if err != nil {
		_ = m.Close()
		return nil, "", err
	}
	return m, "/dev/pts/" + strconv.Itoa(n), nil
}
