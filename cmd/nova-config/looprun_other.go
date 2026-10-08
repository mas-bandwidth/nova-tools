//go:build !linux

package main

import "syscall"

// loopChildAttr is linux's parent-death signal, and no other platform carries
// one: elsewhere the command ends when the wrapper that started it ends by the
// unit's own stop path, and a wrapper killed outright is the platform's limit.
// The rest of loop run -- the lock, the start count, the metrics and the exit
// code -- is the same shape on every platform.
func loopChildAttr() *syscall.SysProcAttr { return nil }
