//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package main

import "os"

func shellTerminal(*os.File) bool { return false }
