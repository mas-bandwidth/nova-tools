//go:build windows

package swarm

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// The process layer on Windows.
// Windows is a platform this repo publishes a binary for, and the honest shape here is a
// degraded one rather than a pretended one: a job is its child process, the group is that
// process, and the survivor check can see nothing beyond it. Every claim this file makes is
// narrower than the unix one, and the places that matter say so on the line they print.
//
// A PID IS NOT AN IDENTITY HERE. Unix ends a process GROUP: `kill(-pgid)` reaches the job
// or nothing, and a group id whose group is gone names nothing. Windows has no group, so
// the same call is a kill BY PID, and Windows re-issues a pid the moment the process ends.
// Under `go test ./...` a dispatcher terminated the two dead pids its slot file recorded
// and the numbers had already been handed to a PARALLEL test's supervisor, which died
// between its harness exiting and its exit.json being written.
//
// So every pid this file is asked about arrives WITH the identity of the process that was
// meant by it: the kernel's creation stamp, from the durable record that recorded the pid
// -- the slot file's `pid_started` and `job_started`, the pid file's, or a stamp taken by
// the parent at `cmd.Start()`. It is a PARAMETER and never a table: the first shape of this
// fix kept one process-global pid->stamp map, and a finished job's stamp could overwrite a
// LIVE job's entry under a re-issued pid, making the dispatcher read a running supervisor
// as dead and finalize it (DeepSeek's read 5, finding 1). An identity that is not owned by
// the job it belongs to is not an identity.

func ownGroup(cmd *exec.Cmd) {}

// Alive reports whether a pid names a live process, and -- where the caller knows which
// process it meant by that pid -- whether it is still that one. A caller with no stamp to
// offer (an older record, another tool's pid) gets the question it asked: is this number
// alive.
func Alive(pid int, started string) bool {
	if pid <= 0 {
		return false
	}
	return livePid(pid) && stillTheSame(pid, started)
}

// GroupAlive reports whether the leader is alive; there is no group to ask about, and a
// leader whose identity the caller cannot supply is not one this platform will claim is
// alive: the answer would be about a number.
func GroupAlive(pgid int, started string) bool {
	if pgid <= 0 || !known(started) {
		return false
	}
	return livePid(pgid) && stillTheSame(pgid, started)
}

// TerminateGroup asks the process to stop.
func TerminateGroup(pgid int, started string) { killPid(pgid, started) }

// KillGroup ends the process.
func KillGroup(pgid int, started string) { killPid(pgid, started) }

// killPid ends a pid ONLY while it still names the process the caller meant. A pid with no
// identity beside it is left alone: on this platform the number outlives the process by no
// time at all, and the wrong process is somebody else's still-running job.
func killPid(pid int, started string) {
	if pid <= 0 || !known(started) || !stillTheSame(pid, started) {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

func known(started string) bool { return started != "" && started != Dash }

func stillTheSame(pid int, started string) bool {
	if !known(started) {
		return true
	}
	return StartStamp(pid) == started
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
