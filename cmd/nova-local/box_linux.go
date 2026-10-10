package main

import (
	"os"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
)

func localBox() Box {
	return Box{
		Load1: hostload.Local().Load1,
		Mem: func() (uint64, uint64, bool) {
			raw, err := os.ReadFile("/proc/meminfo")
			if err != nil {
				return 0, 0, false
			}
			return meminfo(string(raw))
		},
	}
}

// meminfo is MemAvailable and MemTotal from /proc/meminfo, in bytes.
func meminfo(s string) (free, total uint64, ok bool) {
	var seen int
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		n, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "MemAvailable:":
			free, seen = n*1024, seen+1
		case "MemTotal:":
			total, seen = n*1024, seen+1
		}
	}
	return free, total, seen == 2
}
