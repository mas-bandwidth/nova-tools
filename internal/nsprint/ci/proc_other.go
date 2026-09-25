//go:build !unix

package ci

import "os/exec"

// checkProcessGroup is the default kill of the check's own process where
// there are no process groups.
func checkProcessGroup(cmd *exec.Cmd) {}
