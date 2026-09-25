//go:build unix

package stream

import (
	"os/exec"
	"syscall"
)

// ownGroup runs the batch test in its own process group and kills the whole
// group on timeout, so a go test child cannot outlive it.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
