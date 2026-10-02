//go:build windows

package main

import "errors"

// diskFree is not measured on Windows: a Windows machine is a client of the swarm, never a
// bench (windowsIsNotABench), so a member there refuses to start a card, saying why.
func diskFree(string) (uint64, error) {
	return 0, errors.New("free disk is not measured on Windows, which is a client of the swarm and never a bench")
}

// diskSize is not measured on Windows either (diskFree).
func diskSize(string) (uint64, error) {
	return 0, errors.New("the volume's size is not measured on Windows, which is a client of the swarm and never a bench")
}
