//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package main

import (
	"syscall"
	"time"
)

func sysAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

func usableCPUs() ([]int, error) { return nil, nil }

func smtGroups([]int) [][]int { return nil }

func pin([]int) error { return nil }

func cpuBusyPct(time.Duration) (int, bool) { return 0, false }
