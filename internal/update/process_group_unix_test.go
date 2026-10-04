//go:build !windows

package update

import (
	"os/exec"
	"syscall"
)

// The kill reaches the PROCESS GROUP, not just the child: a grandchild of the
// process we killed would otherwise outlive it, and the death a test stages
// would not be the death it claims.
func setGroup(c *exec.Cmd)          { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func assignGroup(c *exec.Cmd) error { return nil }
func killGroup(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
}
