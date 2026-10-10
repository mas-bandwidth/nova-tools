//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package tty

import (
	"os"

	"golang.org/x/sys/unix"
)

// IsTerminal says whether f is a terminal. A character device alone is not
// (/dev/null is one too): the terminal is what answers for its window size.
func IsTerminal(f *os.File) bool {
	_, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	return err == nil
}

// Size is the rows and columns of f's screen, each 0 when not known.
func Size(f *os.File) (rows, cols int) {
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0
	}
	return int(ws.Row), int(ws.Col)
}
