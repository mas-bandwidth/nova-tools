package main

import "syscall"

// sysAttr has the kernel SIGKILL the command when nova-reserve dies, however it dies.
func sysAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL} }
