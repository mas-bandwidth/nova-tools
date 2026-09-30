package tty

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// IsTerminal says whether f is a console.
func IsTerminal(f *os.File) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode) == nil
}

// Size is the rows and columns of f's visible window, each 0 when not known.
func Size(f *os.File) (rows, cols int) {
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(f.Fd()), &info); err != nil {
		return 0, 0
	}
	return int(info.Window.Bottom-info.Window.Top) + 1, int(info.Window.Right-info.Window.Left) + 1
}
