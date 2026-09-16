//go:build !windows

package pulse

import (
	"os/exec"
	"syscall"
)

// detach puts a started child in a process group of its own, so the batch (and the launch
// check) outlive this verb and a signal to this process group does not take the pulse's
// cards down with it.
func detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
