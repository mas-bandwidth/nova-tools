//go:build linux

package main

import (
	"errors"
	"net/netip"

	"golang.org/x/sys/unix"
)

// rawClient is the probe's two acts the net package cannot spell: a TCP Fast Open send
// to addr, and listen() on a TCP socket that was never bound.
func rawClient(kind, arg string) error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	switch kind {
	case "tfo":
		ap, err := netip.ParseAddrPort(arg)
		if err != nil || !ap.Addr().Is4() {
			return errors.New("tfo wants an IPv4 host:port")
		}
		tv := unix.Timeval{Sec: 3}
		_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &tv)
		return unix.Sendto(fd, []byte("probe"), unix.MSG_FASTOPEN, &unix.SockaddrInet4{Port: int(ap.Port()), Addr: ap.Addr().As4()})
	case "listen":
		return unix.Listen(fd, 1)
	}
	return errors.New("unknown kind")
}
