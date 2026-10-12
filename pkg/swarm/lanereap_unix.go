//go:build unix

package swarm

import (
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
// lane is its session's group (the session leader's group, pgid == sid): it is reaped whole,
// and found is 1 when anything was in it.
func ReapLane(sid int, mark string, grace time.Duration) (found, survived int) {
	own, err := unix.Getsid(0)
	if err != nil {
		own = -1
	}
	sid = laneSession(sid, own)
	if _, ok := laneProcs(sid, mark); !ok {
		if sid <= 0 || !GroupAlive(sid, "") {
			return 0, 0
		}
		if Reap(sid, "", grace) {
			return 1, 1
		}
		return 1, 0
	}
	seen := map[int]bool{}
	termed := map[int]bool{}
	// round signals every listed process (sig, and SIGCONT after a terminate) and reports
	// whether the lane was empty when listed
	round := func(sig syscall.Signal, once bool) bool {
		pids, _ := laneProcs(sid, mark) // ignored: the first listing above proved this system lists processes
		for _, pid := range pids {
			seen[pid] = true
			if once && termed[pid] {
				continue
			}
			termed[pid] = true
			// ignored: a process gone between the listing and the signal is the end wanted
			_ = syscall.Kill(pid, sig)
			if sig == syscall.SIGTERM {
				_ = syscall.Kill(pid, syscall.SIGCONT) // ignored: as above
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
	left, _ := laneProcs(sid, mark) // ignored: as above
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
