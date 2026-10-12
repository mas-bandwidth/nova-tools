//go:build linux

package swarm

import (
	"os"
	"strings"
)

// LaneStartStamp binds the process start ticks to this boot, so a persisted PID
// record cannot match a later boot's process at the same tick count.
func LaneStartStamp(pid int) string {
	stamp := StartStamp(pid)
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || stamp == "-" || strings.TrimSpace(string(boot)) == "" {
		return "-"
	}
	return strings.TrimSpace(string(boot)) + ":" + stamp
}

func laneMarked(pid int, mark string) bool { return environHolds(pid, []byte(LaneEnv+"="+mark)) }
