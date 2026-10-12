//go:build unix && !linux && !darwin

package swarm

// laneProcs cannot list processes here. ReapLane falls back to the session's group, which a
// process that left the session escapes; the LANE END line's count is 1 or 0 here.
func laneProcs(sid int, mark string) ([]int, bool) { return nil, false }
