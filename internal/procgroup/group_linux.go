//go:build linux

package procgroup

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// GroupRunnable excludes zombies, which cannot execute card code.
func GroupRunnable(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	es, err := os.ReadDir("/proc")
	if err != nil {
		return true
	}
	for _, e := range es {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
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

func processZombie(pid int) bool {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
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
