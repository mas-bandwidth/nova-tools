package friend

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const ProcessIdentitySupported = true

// ProcessIdentity is the kernel boot and process birth, excluding zombies.
// A reused PID cannot acquire the same birth tick in the same boot.
func ProcessIdentity(pid int) string {
	if pid <= 0 {
		return ""
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	i := strings.LastIndexByte(string(raw), ')')
	if i < 0 {
		return ""
	}
	fields := strings.Fields(string(raw[i+1:]))
	// field 3 is state, and field 22 is starttime.
	if len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" {
		return ""
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(boot)) + ":" + fields[19]
}

// ProcessGroupAlive is a conservative witness that a non-zombie member of
// the old session remains. Without its leader's birth proof it cannot be
// adopted, but its card must not be failed while it could still be writing.
func ProcessGroupAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	paths, err := filepath.Glob("/proc/[0-9]*/stat")
	if err != nil {
		return false
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		i := strings.LastIndexByte(string(raw), ')')
		if i < 0 {
			continue
		}
		fields := strings.Fields(string(raw[i+1:]))
		if len(fields) < 4 || fields[0] == "Z" || fields[0] == "X" {
			continue
		}
		group, err := strconv.Atoi(fields[2]) // field 5, process group ID
		if err == nil && group == pid {
			return true
		}
	}
	return false
}
