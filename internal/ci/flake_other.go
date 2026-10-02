//go:build !unix && !windows

package ci

import "os/exec"

func configureFlakeProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		cleanupFlakeProcess(cmd)
		return nil
	}
}

func cleanupFlakeProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return
	}
	_ = cmd.Process.Kill()
}
