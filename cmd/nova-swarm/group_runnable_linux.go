//go:build linux

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// groupRunnable asks /proc whether any member of this group can still execute.
// Zombies hold a group number until reaped but cannot run card code.
func groupRunnable(pgid int) bool {
	es, err := os.ReadDir("/proc")
	if err != nil {
		return true
	}
	for _, e := range es {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile(fmt.Sprintf("/proc/%s/stat", e.Name()))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return true
		}
		i := strings.LastIndexByte(string(b), ')')
		if i < 0 {
			return true
		}
		f := strings.Fields(string(b)[i+1:])
		if len(f) < 3 {
			return true
		}
		group, err := strconv.Atoi(f[2])
		if err != nil {
			return true
		}
		if group == pgid && f[0] != "Z" && f[0] != "X" {
			return true
		}
	}
	return false
}
