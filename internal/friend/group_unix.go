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

// ProcessAlive is whether the process pid is alive: a signal 0 the kernel delivers or
// refuses for want of permission (the process is there), never ESRCH.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// killGroup ends every process left in the group led by pid.
func killGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) } // ignored: a group already gone is the state wanted
