//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package main

import "syscall"

// sysAttr puts the command in its own process group, so a signal reaches
// everything it started, and watchdog everything left after a SIGKILL.
func sysAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

// usableCPUs is none: nova-reserve pins a command to CPUs only on Linux
// (macOS has no CPU affinity to set), so here it runs on every CPU.
func usableCPUs() ([]int, error) { return nil, nil }

// pin is never reached with no usable CPUs.
func pin([]int) error { return nil }
