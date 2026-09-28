//go:build unix

package card

import (
	"os/exec"
	"syscall"
)

// harnessGroup puts the harness at the head of its own process group, so the
// wrapper can stop everything the harness started with one signal.
func harnessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup stops the harness and every process in its group.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
