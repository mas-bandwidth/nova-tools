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
