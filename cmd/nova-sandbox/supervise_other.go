//go:build !windows && !linux

package main

import "syscall"

// procAttr is empty: this system has no Pdeathsig. cmd/nova-sandbox/README.md says what that means.
func procAttr() *syscall.SysProcAttr { return nil }
