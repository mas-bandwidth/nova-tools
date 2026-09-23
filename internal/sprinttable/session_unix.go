//go:build unix

package sprinttable

import (
	"os/exec"
	"syscall"
)

// ApplyOwnSession makes cmd a session leader when it execs.
//
// SysProcAttr.Setsid is POSIX setsid() in the child, before exec. Darwin has
// no setsid(1); this is that call. A child that has called setsid is not in
// the unit's process group, so launchctl kickstart -k — which SIGTERMs the
// group launchd is tracking — does not kill the refresh the unit started.
func ApplyOwnSession(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	return nil
}
