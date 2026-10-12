//go:build unix && !linux && !darwin

package swarm

// laneProcs cannot list process births here, so ReapLane refuses group signalling.
func laneProcs(sid int, mark string, stuck map[int]bool) ([]laneProcess, bool) { return nil, false }

// scanLaneProcs cannot list process births and environments here, so ReapOrphanLanes signals
// none.
func scanLaneProcs(stuck map[int]bool) ([]laneProcInfo, bool) { return nil, false }
