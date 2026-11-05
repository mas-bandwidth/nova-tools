//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package main

import "syscall"

// sysAttr puts the command in its own process group, so an interrupt reaches
// everything it started. There is no Pdeathsig here: a SIGKILL of nova-reserve
// leaves the command running without its slot.
func sysAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
