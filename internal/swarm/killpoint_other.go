//go:build !unix

package swarm

import "os"

// CheckKillPoint checks if NOVA_SWARM_KILLPOINT matches point, and if so,
// terminates the process immediately.
func CheckKillPoint(point string) {
	if kp := os.Getenv("NOVA_SWARM_KILLPOINT"); kp != "" && kp == point {
		os.Exit(137)
	}
}

// CheckPausePoint is a no-op on non-unix systems.
func CheckPausePoint(point string) {}
