//go:build !linux && !darwin

package swarm

// LaneStartStamp reports the kernel identity where StartStamp supports it.
func LaneStartStamp(pid int) string { return StartStamp(pid) }

// This platform cannot inspect a launch mark: it refuses adoption of a live PID.
func laneMarked(pid int, mark string) bool { return false }
