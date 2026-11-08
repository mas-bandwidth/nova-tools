package main

import "syscall"

// sysAttr puts the command in its own process group, so a signal reaches
// everything it started, and watchdog everything left after a SIGKILL;
// Pdeathsig has the kernel SIGKILL the command itself at once.
func sysAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
