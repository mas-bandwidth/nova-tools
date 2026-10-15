package main

import (
	"os"
	"strconv"
	"strings"
)

func localBox() Box {
	return Box{
		Load1: func() (float64, bool) {
			raw, err := os.ReadFile("/proc/loadavg")
			if err != nil {
				return 0, false
			}
			return parseProcLoadavg(raw)
		},
		Mem: func() (uint64, uint64, bool) {
			raw, err := os.ReadFile("/proc/meminfo")
			if err != nil {
				return 0, 0, false
			}
			return meminfo(string(raw))
		},
	}
}

// parseProcLoadavg is the one-minute load from the bytes of /proc/loadavg, whose first
// field is load1. A missing, malformed or negative field is not a reading.
func parseProcLoadavg(b []byte) (float64, bool) {
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil || v < 0 || v != v {
		return 0, false
	}
	return v, true
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
