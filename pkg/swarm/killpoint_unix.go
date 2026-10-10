//go:build unix && swarmtest

package swarm

import (
	"os"
	"time"
)

// THE ORDER OF TWO DEATHS is what the reverse schedule is made of, and a test that
// HOPES for an order gets the other one on a loaded runner: on a shared CI runner,
// `TestTheLaunchIsATransaction/reverse-schedule`, `aborted.json was not written`, twice
// identically, while the same test was green on every Mac and on a linux box with a core
// to itself.
//
// The schedule the test needs is: the runner dies FIRST, and the supervisor stops
// afterwards. The other order is not a schedule anybody can stage -- a process that stops
// BEFORE its parent dies is a stopped member of a process group that the parent's death
// orphans, and POSIX then has the kernel send that group SIGHUP and SIGCONT (linux
// kill_orphaned_pgrp, darwin orphanpg). SIGHUP's default action ends the supervisor where
// it stands, with an empty supervisor.log and no aborted.json -- measured on ubuntu
// 6.8 with the runner's own death delayed 500 ms, and the exact CI evidence.
//
// So the two knobs below let a test FIX the order rather than hope for it, in either
// direction. They are the same test-only machinery as the points themselves: environment
// variables no product path ever sets. Every wait here is bounded and ends on its own.
//
//	NOVA_SWARM_PAUSE_AFTER_ORPHAN  the pause point waits until its parent is gone before
//	                               it stops, so the kernel has nothing stopped to hang up
//	NOVA_SWARM_PAUSE_MARK          the pause point creates this file immediately before it
//	                               stops, so another process can wait for the stop
const injectedWait = 10 * time.Second

// startParent is the pid of the process that FORKED this one, read at program start
// because that is the only moment it is certainly still there. Read at the pause point
// instead it is often already 1 -- the runner dies a millisecond after the fork and the
// child needs ten to reach any code of its own -- and a wait for "the parent I have now to
// change" then waits for its whole deadline and calls that an orphan.
var startParent = os.Getppid()

// waitForOrphan waits until the process that forked this one is gone, and gives up on its
// own.
func waitForOrphan(within time.Duration) {
	// ALREADY AN ORPHAN, which is the ordinary case rather than the odd one: the runner's
	// injected death lands a microsecond after the fork and a new process needs a
	// millisecond to reach any code of its own, so by the time this package initialised,
	// the parent was init. Measured on ubuntu 6.8: startParent=1 every time, and a wait
	// for "the parent I started with to change" then waits for its whole deadline and
	// calls that an orphan.
	if startParent <= 1 {
		return
	}
	for waited := time.Duration(0); waited < within; waited += 5 * time.Millisecond {
		if os.Getppid() != startParent {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}
