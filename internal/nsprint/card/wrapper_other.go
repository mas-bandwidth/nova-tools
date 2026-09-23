//go:build !unix

package card

import "os/exec"

// harnessGroup has no process group to set outside unix; cards run on unix
// benches only.
func harnessGroup(*exec.Cmd) {}

// killGroup stops the harness process itself.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
