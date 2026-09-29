//go:build unix

package ci

import (
	"os/exec"
	"syscall"
)

func configureFlakeProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		cleanupFlakeProcess(cmd)
		return nil
	}
}

func cleanupFlakeProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return
	}
	pgid := cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
