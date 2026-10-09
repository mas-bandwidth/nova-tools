//go:build !linux

package main

func markInheritedFDsCloseOnExec() error { return nil }
