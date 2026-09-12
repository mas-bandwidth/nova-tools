//go:build !windows

package update

import (
	"os/exec"
	"syscall"
)

// The wrapper kills the PROCESS GROUP, not just nova-bus: a Git child of the
// binary we killed would otherwise finish a push nobody is waiting on, and the
// death this test stages would not be the death it claims.
func setGroup(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func killGroup(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
}

// groupGone answers the question the lock repair turns on: is there still a
// process that could own the git index lock? Signal 0 asks the kernel without
// sending anything, and ESRCH is the whole group being gone.
func groupGone(c *exec.Cmd) bool {
	if c.Process == nil {
		return true
	}
	return syscall.Kill(-c.Process.Pid, syscall.Signal(0)) == syscall.ESRCH
}
