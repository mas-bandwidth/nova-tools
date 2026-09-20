//go:build !unix

package pulse

import "os/exec"

// BoundObservation still bounds the pipe-copy wait where process groups are
// not a thing. WaitDelay is what makes the budget real on this path.
func BoundObservation(cmd *exec.Cmd) {
	cmd.WaitDelay = ObservationWaitDelay
}
