//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// configureCheckProcess puts a check in its own process group, so that a check which
// spawns children -- a build that starts a linker, a test that starts a server -- can be
// killed whole rather than leaving the tree running after --timeout says stop.
func configureCheckProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killCheckProcess kills the whole group by its negative pid, then the leader in case
// the group call did not reach it.
func killCheckProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}

// shellCommand runs a check through the platform shell, because --checks is a
// comma-separated list of command lines as a person would type them.
func shellCommand(check string) (string, []string) {
	return "sh", []string{"-c", check}
}
