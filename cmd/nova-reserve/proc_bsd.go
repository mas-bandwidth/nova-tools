//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package main

import "syscall"

// sysAttr puts the command in its own process group, so a signal reaches
// everything it started, and watchdog everything left after a SIGKILL.
func sysAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
