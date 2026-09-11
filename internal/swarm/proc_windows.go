//go:build windows

package swarm

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// The process layer on Windows.
// Windows is a platform this repo publishes a binary for, and the honest shape here is a
// degraded one rather than a pretended one: a job is its child process, the group is that
// process, and the survivor check can see nothing beyond it. Every claim this file makes is
// narrower than the unix one, and the places that matter say so on the line they print.

func ownGroup(cmd *exec.Cmd) {}

// Alive reports whether a pid names a live process.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
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

// GroupAlive reports whether the leader is alive; there is no group to ask about.
func GroupAlive(pgid int) bool { return Alive(pgid) }

func killPid(pid int) {
	if pid <= 0 {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

// StartStamp is the kernel's start stamp for a pid, and there is none to be had here
// without a package outside the standard library. The dash is the honest answer, and the
// comparison that uses it compares dash with dash.
func StartStamp(pid int) string { return "-" }

// GroupMembers counts the processes in a group other than self. Unavailable here.
func GroupMembers(pgid, self int) (int, bool) { return 0, false }

// pgidOf has no process group to report here, so a process is its own group of one.
func pgidOf(pid int) int { return pid }
