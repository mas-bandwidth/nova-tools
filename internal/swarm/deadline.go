package swarm

import "time"

// THE DEADLINE IS HELD BY THE MACHINERY, NOT BY THE WORKER.
//
// A worker asked to enforce its own deadline is a worker whose deadline depends on the thing
// that has stopped responding -- and a worker asked to loop ran until somebody noticed. So
// the holder is outside: the supervisor beside the harness, or an adopting dispatcher from
// the recorded start.
//
// A WAIT LOOP NEVER ENDS BY SCANNING FOR ITS OWN NAME. The reap below signals a process
// GROUP this process started and recorded. It never matches a process by its command line:
// that is how 19 orphaned shells happened on 2026-09-10, a loop having found itself.

// TerminateGrace is how long a group is given to stop after the terminate and before the
// kill. Long enough that a harness can flush what it was writing, short enough that a reap
// is a reap.
const TerminateGrace = 3 * time.Second

// Reap ends a process group: terminate, wait, kill -- and then reports whether anything in
// it SURVIVED, because a slot whose data home may still have a writer in it is not a free
// slot, and a pool that re-used it would reproduce the lock failure with a corpse.
func Reap(pgid int, started string, grace time.Duration) (survived bool) {
	if pgid <= 0 {
		return false
	}
	TerminateGroup(pgid, started)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !GroupAlive(pgid, started) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !GroupAlive(pgid, started) {
		return false
	}
	KillGroup(pgid, started)
	// The kill is not instant: the kernel reaps at its own pace, and a check that ran in
	// the same microsecond would report a survivor that is a corpse.
	deadline = time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !GroupAlive(pgid, started) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
	return GroupAlive(pgid, started)
}
