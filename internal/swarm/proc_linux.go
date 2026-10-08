//go:build linux

package swarm

import (
	"fmt"
	"os"
	"strings"
)

// StartStamp combines this boot's ID and the process's start tick (field 22
// of /proc/<pid>/stat). A receipt from another boot cannot authorize a reused
// PID even if its tick happens to match.
func StartStamp(pid int) string {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || strings.TrimSpace(string(boot)) == "" {
		return "-"
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "-"
	}
	// The comm field is in parentheses and may hold spaces and parentheses of its own, so
	// the split starts after the LAST ")". Reading the name at all is reading a field the
	// kernel wrote, never matching a command line.
	i := strings.LastIndex(string(raw), ")")
	if i < 0 {
		return "-"
	}
	fields := strings.Fields(string(raw)[i+1:])
	// fields[0] is state, which is field 3; field 22 is therefore fields[19].
	if len(fields) < 20 {
		return "-"
	}
	return strings.TrimSpace(string(boot)) + ":" + fields[19]
}
