//go:build unix

package swarm

import (
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// ReapLane ends every live process of a lane (lanereap.go): the processes in session sid and
// the processes whose environment holds LaneEnv=mark. Each is asked to stop (a terminate, and
// a continue so a stopped one can act on it), the lane is given grace to empty, then what is
// left is killed and given grace again. The lane is listed afresh each round, so a process
// forked during the reap is ended too. found is how many distinct processes of the lane were
// alive and signalled; survived is how many still are at the end (one this user may not
// signal, or one the kernel has not let go of). Where this system cannot list processes the
// reaper refuses to signal a numeric group without a birth-bearing listing.
func ReapLane(sid int, mark, started string, grace time.Duration) (found, survived int) {
	own, err := unix.Getsid(0)
	if err != nil {
		own = -1
	}
	sid = laneSession(sid, own)
	// A reused leader PID authorizes no session. A departed leader leaves its
	// session alive while descendants remain; recheck before each listing below.
	ownedSession := func() int {
		if started == "" || started == "-" {
			return 0
		}
		if live := LaneStartStamp(sid); live != "-" && live != started {
			return 0
		}
		return sid
	}
	if _, ok := laneProcs(ownedSession(), mark); !ok {
		// No birth-bearing listing means no authority to signal a numeric group.
		// A group signal cannot bind all its targets to the recorded process births.
		return 0, 0
	}
	seen := map[laneProcess]bool{}
	termed := map[laneProcess]bool{}
	// round signals every listed process (sig, and SIGCONT after a terminate) and reports
	// whether the lane was empty when listed
	round := func(sig syscall.Signal, once bool) bool {
		pids, _ := laneProcs(ownedSession(), mark) // ignored: the first listing above proved this system lists processes
		for _, target := range pids {
			seen[target] = true
			if once && termed[target] {
				continue
			}
			termed[target] = true
			// ignored: a process gone between the listing and the signal is the end wanted
			_ = signalLaneProcess(target, sig)
			if sig == syscall.SIGTERM {
				_ = signalLaneProcess(target, syscall.SIGCONT) // ignored: as above
			}
		}
		return len(pids) == 0
	}
	for _, phase := range []struct {
		sig  syscall.Signal
		once bool
	}{{syscall.SIGTERM, true}, {syscall.SIGKILL, false}} {
		deadline := time.Now().Add(grace)
		for {
			if round(phase.sig, phase.once) {
				return len(seen), 0
			}
			if !time.Now().Before(deadline) {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	left, _ := laneProcs(ownedSession(), mark) // ignored: as above
	return len(seen), len(left)
}

// laneSession is the session ReapLane ends: sid, unless it is the asking process's own (own;
// -1 when unknown, which ends no session), for a lane's session is its native's and never the
// member's. 0 ends none.
func laneSession(sid, own int) int {
	if sid <= 0 || own < 0 || sid == own {
		return 0
	}
	return sid
}

// laneProcess is the birth observed while listing, not just a reusable PID.
type laneProcess struct {
	pid     int
	started string
}

// signalLaneProcess opens the OS process handle before revalidating its birth.
// Linux uses the Go runtime's pidfd handle; systems without one revalidate the
// kernel birth immediately before signalling but cannot make those two calls atomic.
func signalLaneProcess(target laneProcess, sig os.Signal) error {
	if target.started == "" || target.started == "-" {
		return os.ErrProcessDone
	}
	proc, err := os.FindProcess(target.pid)
	if err != nil {
		return err
	}
	defer proc.Release() // ignored: releases this inspection handle only
	if LaneStartStamp(target.pid) != target.started {
		return os.ErrProcessDone
	}
	return proc.Signal(sig)
}
