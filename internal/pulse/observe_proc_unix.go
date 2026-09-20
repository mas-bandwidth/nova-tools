//go:build unix

package pulse

import (
	"os/exec"
	"syscall"
)

// BoundObservation is the kill-and-wait policy every observation child carries.
// CommandContext kills only the process it started; bytes.Buffer and CombinedOutput
// copy pipes stay open while descendants inherit them. Setpgid plus SIGKILL to
// the group reaps those descendants, and WaitDelay closes the pipes if anything
// still holds them.
func BoundObservation(cmd *exec.Cmd) {
	cmd.WaitDelay = ObservationWaitDelay
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
