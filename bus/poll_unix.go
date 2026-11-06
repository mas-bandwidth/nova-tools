//go:build darwin || linux

package main

// pollFd is struct pollfd. The poll asks for POLLOUT, because Darwin reports
// a pipe's lost reader (POLLHUP) only to a poll that asks for something;
// pollGone is the revents that say fd 1 has no reader: POLLERR, POLLHUP,
// POLLNVAL (the same values on Darwin and Linux).
type pollFd struct {
	fd              int32
	events, revents int16
}

const (
	pollOut  = 0x4
	pollGone = 0x8 | 0x10 | 0x20
)
