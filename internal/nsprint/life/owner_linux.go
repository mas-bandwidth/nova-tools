//go:build linux

package life

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ProbeProcess reads the kernel's start ticks and boot identity. A zombie
// cannot renew a copy. Permission/read errors are unknown, not process death.
func ProbeProcess(pid int) ProcessSample {
	if pid <= 0 {
		return ProcessSample{Err: fmt.Errorf("invalid pid")}
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if os.IsNotExist(err) {
		return ProcessSample{Absent: true}
	}
	if err != nil {
		return ProcessSample{Err: err}
	}
	i := strings.LastIndex(string(raw), ")")
	if i < 0 {
		return ProcessSample{Err: fmt.Errorf("malformed proc stat")}
	}
	f := strings.Fields(string(raw[i+1:]))
	if len(f) < 20 {
		return ProcessSample{Err: fmt.Errorf("short proc stat")}
	}
	if f[0] == "Z" || f[0] == "X" {
		return ProcessSample{Absent: true}
	}
	if _, err := strconv.ParseUint(f[19], 10, 64); err != nil {
		return ProcessSample{Err: err}
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ProcessSample{Err: err}
	}
	b := strings.TrimSpace(string(boot))
	if b == "" {
		return ProcessSample{Err: fmt.Errorf("empty boot identity")}
	}
	return ProcessSample{Start: b + ":" + f[19]}
}
