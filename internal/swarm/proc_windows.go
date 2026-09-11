//go:build windows

package swarm

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
)

// The process layer on Windows.
// Windows is a platform this repo publishes a binary for, and the honest shape here is a
// degraded one rather than a pretended one: a job is its child process, the group is that
// process, and the survivor check can see nothing beyond it. Every claim this file makes is
// narrower than the unix one, and the places that matter say so on the line they print.
//
// A PID IS NOT AN IDENTITY HERE. Unix kills a process GROUP -- `kill(-pgid)` reaches the
// job or nothing, and an id whose group is gone names nothing. Windows has no group to
// name, so the same call is a kill BY PID, and Windows hands a pid back for reuse the
// moment the process ends. On 2026-09-11 CI that cost two jobs: a dispatcher finished one
// job, terminated the two dead pids its slot file recorded, and the numbers had already
// been handed to a PARALLEL test's supervisor, which died between its harness exiting and
// its exit.json being written -- `rc=-1`, the sentinel for "no completion evidence", on a
// job that had finished cleanly.
//
// So every pid this file may END carries its identity beside it: the kernel's creation
// stamp, learned when the process was ours (`noteChild`) or read from the durable record
// that named it (`identify`). A pid whose stamp no longer matches is somebody else's
// process, and this file will not touch it, ask about its group, or call it alive.

// known is pid -> the creation stamp this run learned for it. It is per-process and it is
// never read from a pid alone: an entry is proof only while the kernel still agrees.
var known sync.Map

// noteChild records a child THIS process started, at the one moment its identity is not in
// doubt. Everything that may later terminate that pid asks this first.
func noteChild(pid int) {
	if pid <= 0 {
		return
	}
	if stamp := StartStamp(pid); stamp != Dash {
		known.Store(pid, stamp)
	}
}

// identify records the identity a durable record carries for a pid this process did not
// start -- a slot file's or a pid file's start stamp -- so that a dispatcher adopting
// somebody else's job can still end it, and still refuse to end a stranger wearing its
// number. On unix this is a no-op: a process group is identity enough.
func identify(pid int, started string) {
	if pid <= 0 || started == "" || started == Dash {
		return
	}
	known.Store(pid, started)
}

// identified reports whether a pid still names the process this run meant by it. No record
// is NOT proof: a pid nobody here established is never ended and never counted alive as a
// group, because on this platform it may be anybody.
func identified(pid int) bool {
	want, ok := known.Load(pid)
	if !ok {
		return false
	}
	return want.(string) == StartStamp(pid)
}

func ownGroup(cmd *exec.Cmd) {}

// Alive reports whether a pid names a live process -- and, where this run knows which
// process it meant by that pid, whether it is still that one.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if !livePid(pid) {
		return false
	}
	if want, ok := known.Load(pid); ok {
		return want.(string) == StartStamp(pid)
	}
	return true
}

func livePid(pid int) bool {
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION|syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		// ERROR_ACCESS_DENIED (5) means the process exists and is alive, but we cannot query it.
		return errors.Is(err, syscall.Errno(5))
	}
	defer syscall.CloseHandle(h)
	event, err := syscall.WaitForSingleObject(h, 0)
	if err != nil {
		return false
	}
	return event == syscall.WAIT_TIMEOUT
}

// TerminateGroup asks the process to stop.
func TerminateGroup(pgid int) { killPid(pgid) }

// KillGroup ends the process.
func KillGroup(pgid int) { killPid(pgid) }

// GroupAlive reports whether the leader is alive; there is no group to ask about, and a
// leader this run cannot identify is not one it will claim is alive.
func GroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	return identified(pgid) && livePid(pgid)
}

// killPid ends a pid ONLY while it still names the process this run meant. An unidentified
// pid is left alone: on this platform the number outlives the process by no time at all.
func killPid(pid int) {
	if pid <= 0 || !identified(pid) {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

// StartStamp is the kernel's start stamp for a pid: the creation FILETIME, which is what
// tells this run's child from the stranger the kernel handed its number to a millisecond
// later. A stamp that cannot be had is the dash, and the comparison that uses it compares
// dash with dash.
func StartStamp(pid int) string {
	if pid <= 0 {
		return Dash
	}
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return Dash
	}
	defer syscall.CloseHandle(h)
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return Dash
	}
	return strconv.FormatUint(uint64(creation.HighDateTime)<<32|uint64(creation.LowDateTime), 10)
}

// GroupMembers counts the processes in a group other than self. Unavailable here.
func GroupMembers(pgid, self int) (int, bool) { return 0, false }

// pgidOf has no process group to report here, so a process is its own group of one.
func pgidOf(pid int) int { return pid }
