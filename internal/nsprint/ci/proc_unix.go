//go:build unix

package ci

import (
	"os/exec"
	"syscall"
)

// checkProcessGroup puts a check in its own process group and makes the
// context's cancel (timeout, or the run stopping) kill the whole group, so a
// `go test` and the test binaries it started go together.
func checkProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return cmd.Process.Kill()
	}
}
