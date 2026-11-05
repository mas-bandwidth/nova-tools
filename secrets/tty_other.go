//go:build !windows && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package main

import "golang.org/x/sys/unix"

const termiosGet = unix.TCGETS
