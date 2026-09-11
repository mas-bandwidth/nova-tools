//go:build unix

package swarm

import (
	"errors"
	"os/exec"
	"syscall"
)

// The process layer, on the systems this tool grew up on.
//
// A job is ONE blocking process in its OWN process group (rule 11), so that a deadline is
// enforced against everything the job started and a dispatcher's own death does not take a
// worker's twenty minutes of reading with it. Nothing here ever matches a process by its
// command line: that is how 19 orphaned shells happened on 2026-09-10, a loop having found
// itself, and the tripwire in the tests looks for pgrep, ps and a command-line compare.

// ownGroup makes the child the leader of a new process group.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// Alive reports whether a pid names a live process. Signal 0 asks the kernel and sends
// nothing; EPERM is a live process this user may not signal, which is still alive.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// TerminateGroup asks every process in a group to stop.
func TerminateGroup(pgid int) {
	if pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
	}
}

// KillGroup ends every process in a group.
func KillGroup(pgid int) {
	if pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

// GroupAlive reports whether ANY process remains in a group.
func GroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// pgidOf is the process group a pid is in.
func pgidOf(pid int) int {
	if pgid, err := syscall.Getpgid(pid); err == nil {
		return pgid
	}
	return pid
}
