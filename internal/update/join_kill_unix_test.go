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
