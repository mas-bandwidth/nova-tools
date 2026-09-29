//go:build windows

package ci

import (
	"os/exec"
	"strconv"
)

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
	killCmd := exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
	_ = killCmd.Run()
	_ = cmd.Process.Kill()
}
