//go:build linux

package main

import (
	"fmt"
	"os"
	"strings"
)

// processZombie distinguishes a reaped run's zombie from a live native child.
func processZombie(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 {
		return false
	}
	f := strings.Fields(string(b)[i+1:])
	return len(f) > 0 && (f[0] == "Z" || f[0] == "X")
}
