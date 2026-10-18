package sandbox

import (
	"os"
	"strconv"
	"strings"
)

// machineRAM is MemTotal from /proc/meminfo in bytes, 0 when it cannot be read.
func machineRAM() int64 {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	return memTotal(string(raw))
}

// memTotal is the MemTotal line of a /proc/meminfo text in bytes (the kernel prints kB),
// 0 when there is none.
func memTotal(meminfo string) int64 {
	for _, line := range strings.Split(meminfo, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "MemTotal:" {
			kb, err := strconv.ParseInt(f[1], 10, 64)
			if err != nil || kb <= 0 {
				return 0
			}
			return kb << 10
		}
	}
	return 0
}
