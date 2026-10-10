//go:build !windows

package functionalrun

import (
	"os/exec"
	"syscall"
)

// setOwnProcessGroup starts the runtime's client in a process group of its
// own, so a terminal's interrupt reaches this tool, which removes the
// container, and never the client alone.
func setOwnProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
