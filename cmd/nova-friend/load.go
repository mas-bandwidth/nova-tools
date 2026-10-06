package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// oneMinuteLoad is the machine's 1-minute load average: /proc/loadavg where there is one,
// else `sysctl -n vm.loadavg` ("{ 1.50 2.00 2.50 }"); 0 when neither answers, so a machine
// whose load cannot be read is never held down by it.
func oneMinuteLoad() float64 {
	if raw, err := os.ReadFile("/proc/loadavg"); err == nil {
		return firstLoad(string(raw))
	}
	out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
	if err != nil {
		return 0
	}
	return firstLoad(string(out))
}

func firstLoad(s string) float64 {
	for _, f := range strings.Fields(strings.Trim(strings.TrimSpace(s), "{}")) {
		if v, err := strconv.ParseFloat(f, 64); err == nil {
			return v
		}
	}
	return 0
}
