//go:build unix

package deal

import (
	"os/exec"
	"syscall"
)

// ownGroup makes the ssh child the leader of its own process group, so the
// deadline reaches everything the child started (a ProxyCommand, a
// ControlMaster it forked) and not the child alone (#3322).
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup ends every process in the child's group. It is cmd.Cancel: the
// hard deadline's kill, run when the session's context ends.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
