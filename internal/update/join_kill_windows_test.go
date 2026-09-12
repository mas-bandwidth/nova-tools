//go:build windows

package update

import "os/exec"

func setGroup(c *exec.Cmd) {}
func killGroup(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	return c.Process.Kill()
}

// Windows has no process group to ask about here, so this reports only what Wait
// already established. The owed Windows termination validation is named in the
// pull request; no test in this file claims it.
func groupGone(c *exec.Cmd) bool { return true }
