//go:build !unix

package swarm

import "time"

// ReapLane has no session to end where there is no process group (proc_other.go,
// proc_windows.go): a lane there is its native process alone, which the member waited on.
func ReapLane(sid int, mark, started string, grace time.Duration) (found, survived int) { return 0, 0 }

// ReapOrphanLanes ends nothing where there is no process session and no environment to read
// (proc_other.go, proc_windows.go).
func ReapOrphanLanes(slots, as string, owned func(mark string) bool, grace time.Duration) (lanes, found, survived int) {
	return 0, 0, 0
}
