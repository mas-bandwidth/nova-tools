//go:build linux

package main

import "syscall"

// loopChildAttr ties the command's life to this process on linux: the kernel
// sends the command SIGKILL when the thread that started it dies, so the death
// of the wrapper that holds the loop's lock ends the command instead of
// orphaning it. Without it, a wrapper killed outright leaves its command
// running with no lock holder, and the restarted unit runs a second command
// beside it. The signal is the kernel's; the command cannot ignore it.
func loopChildAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
