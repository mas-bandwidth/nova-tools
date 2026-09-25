//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// powerSetProcessGroup makes the child its own process group leader, so a script that
// spawns children can be killed whole.
func powerSetProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// powerKillProcessGroup kills the child and everything in its process group. A timeout
// must not leave a remote script's children behind.
func powerKillProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
