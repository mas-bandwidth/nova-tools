//go:build windows

package deal

import "os/exec"

// Windows has no process group to lead: the deadline reaches the ssh child
// alone, and the honest shape here is the narrower one (#3322).
func ownGroup(*exec.Cmd) {}

// killGroup ends the child process, the whole of what Windows can see.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
