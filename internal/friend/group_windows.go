//go:build windows

package friend

import "os/exec"

// ownGroup is a no-op on Windows: no process group is set, and a cancel
// ends the one process (the harnesses this tool delivers to run on Unix).
func ownGroup(*exec.Cmd) {}

func killGroup(int) {}

// GroupAlive is always false on Windows: there is no group to ask.
func GroupAlive(int) bool { return false }
