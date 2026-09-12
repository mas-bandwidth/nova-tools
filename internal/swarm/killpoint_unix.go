//go:build unix

package swarm

import (
	"os"
	"syscall"
	"time"
)

// THE ORDER OF TWO DEATHS is what rule 18's reverse schedule is made of, and a test that
// HOPES for an order gets the other one on a loaded runner: ubuntu CI, 2026-09-12,
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
//	NOVA_SWARM_KILL_AFTER          the kill point waits for that file, and a moment more
//	                               for the stop itself, before it kills: the OTHER order,
//	                               staged on purpose by the test that names the hangup
const injectedWait = 10 * time.Second

// injectedStopGrace is how long the kill point waits after the mark appears. The mark says
// "about to stop" and the stop is the next instruction; the grace is four orders of
// magnitude more than that takes on any machine this has been measured on.
const injectedStopGrace = 100 * time.Millisecond

// CheckKillPoint checks if NOVA_SWARM_KILLPOINT matches point, and if so,
// terminates the process immediately using SIGKILL.
func CheckKillPoint(point string) {
	if kp := os.Getenv("NOVA_SWARM_KILLPOINT"); kp != "" && kp == point {
		if mark := os.Getenv("NOVA_SWARM_KILL_AFTER"); mark != "" {
			waitForInjectedFile(mark, injectedWait)
			time.Sleep(injectedStopGrace)
		}
		if point == "between-exit-and-exit-json" || point == "supervisor-after-release" {
			ppid := os.Getppid()
			if ppid > 1 {
				_ = syscall.Kill(ppid, syscall.SIGKILL)
			}
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		os.Exit(137)
	}
}

// CheckPausePoint checks if NOVA_SWARM_PAUSEPOINT matches point, and if so,
// stops the process with SIGSTOP until resumed with SIGCONT.
func CheckPausePoint(point string) {
	if pp := os.Getenv("NOVA_SWARM_PAUSEPOINT"); pp != "" && pp == point {
		if os.Getenv("NOVA_SWARM_PAUSE_AFTER_ORPHAN") != "" {
			waitForOrphan(injectedWait)
		}
		if mark := os.Getenv("NOVA_SWARM_PAUSE_MARK"); mark != "" {
			_ = os.WriteFile(mark, []byte("about to stop\n"), 0o644)
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGSTOP)
	}
}

// waitForInjectedFile waits for a marker another process writes, and gives up on its own.
func waitForInjectedFile(path string, within time.Duration) {
	for waited := time.Duration(0); waited < within; waited += 5 * time.Millisecond {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

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
