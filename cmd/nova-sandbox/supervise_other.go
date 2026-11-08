//go:build !windows && !linux

package main

import "syscall"

// procAttr is empty: this system has no Pdeathsig. The SPEC says what that means.
func procAttr() *syscall.SysProcAttr { return nil }
