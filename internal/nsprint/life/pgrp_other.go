//go:build !unix

package life

import "os/exec"

func ownGroup(cmd *exec.Cmd) {}

func killGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
