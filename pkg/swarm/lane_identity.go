package swarm

// LaneIdentityMatches proves both the birth recorded for a launch and its exact
// environment mark. A missing kernel identity is never a match (SPEC-SWARM, member).
func LaneIdentityMatches(pid int, started, mark string) bool {
	return pid > 1 && started != "" && started != "-" && mark != "" &&
		LaneStartStamp(pid) == started && laneMarked(pid, mark) && LaneStartStamp(pid) == started
}
