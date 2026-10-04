//go:build linux

package swarm

import (
	"fmt"
	"os"
	"strings"
)

// StartStamp is the kernel's own start stamp for a pid: field 22 of /proc/<pid>/stat, the
// process's start time in clock ticks since boot. It is what tells a live pid from a REUSED
// one -- a slot file whose pid is alive under a different stamp is a different process
// wearing an old number, and rule 17 quarantines it rather than adopting it.
func StartStamp(pid int) string {
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
	return fields[19]
}
