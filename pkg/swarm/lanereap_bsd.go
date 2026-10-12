//go:build unix && !linux && !darwin

package swarm

// laneProcs cannot list process births here, so ReapLane refuses group signalling.
func laneProcs(sid int, mark string) ([]laneProcess, bool) { return nil, false }

// scanLaneProcs cannot list process births and environments here, so ReapOrphanLanes signals
// none.
func scanLaneProcs() ([]laneProcInfo, bool) { return nil, false }
