//go:build unix

package friend

import (
	"os/exec"
	"syscall"
)

// ownGroup makes cmd a session leader of its own, so a Cancel signals the
// group (every process the harness forked) and the caller's WaitDelay then
// kills it.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}

// killGroup ends every process left in the group led by pid.
func killGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) } // ignored: a group already gone is the state wanted
