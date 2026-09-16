//go:build windows

package pulse

import "os/exec"

// detach is a no-op on windows: there are no process groups of this shape, and the child is
// released by not being waited on.
func detach(cmd *exec.Cmd) {}
