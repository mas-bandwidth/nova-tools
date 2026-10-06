//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// ownLandGroup makes cmd the leader of a process group of its own, and makes a
// cancelled context end the whole group: the landing step's go, sh and git
// children each carry everything they start, so a killed gate takes its
// compiled test binary with it.
func ownLandGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

// killLandGroup ends every process in the group led by pgid.
func killLandGroup(pgid int) {
	if pgid > 0 {
		// ignored: a group already gone is the state wanted
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

// landGroupAlive reports whether any process remains in the group led by pgid.
func landGroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
