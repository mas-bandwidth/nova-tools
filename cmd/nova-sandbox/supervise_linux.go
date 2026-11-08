//go:build linux

package main

import "syscall"

// procAttr kills the command when the waiter dies, including under SIGKILL.
func procAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
