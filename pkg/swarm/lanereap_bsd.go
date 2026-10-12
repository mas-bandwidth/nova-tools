//go:build unix && !linux && !darwin

package swarm

// laneProcs cannot list process births here, so ReapLane refuses group signalling.
func laneProcs(sid int, mark string) ([]laneProcess, bool) { return nil, false }
