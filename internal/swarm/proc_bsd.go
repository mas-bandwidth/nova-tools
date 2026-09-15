//go:build unix && !linux && !darwin

package swarm

// StartStamp has no standard-library route on this system, and a dash is the honest
// answer: the comparison that uses it compares dash with dash, and a reused pid is
// quarantined by the other tests rule 17 makes.
func StartStamp(pid int) string { return "-" }

// GroupMembers cannot enumerate a group here. The caller falls back to GroupAlive, which
// answers the weaker question -- is anything left in the group at all.
func GroupMembers(pgid, self int) (int, bool) { return 0, false }

// ProcTreeActivity has no standard-library route to the CPU/I/O counters here. ok=false says
// the measurement could not be made, and the idle watch falls back to the card's log alone.
func ProcTreeActivity(pid int) (cpu, io int64, ok bool) { return 0, 0, false }
