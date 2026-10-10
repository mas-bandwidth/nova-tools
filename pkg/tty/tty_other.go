//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package tty

import "os"

// IsTerminal is false here: nothing answers for a terminal.
func IsTerminal(*os.File) bool { return false }

// Size is not known here.
func Size(*os.File) (rows, cols int) { return 0, 0 }
