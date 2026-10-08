//go:build windows

package friend

import "os/exec"

// ownGroup is a no-op on Windows: no process group is set, and a cancel
// ends the one process (the harnesses this tool delivers to run on Unix).
func ownGroup(*exec.Cmd) {}

func gateProcess(*exec.Cmd, bool) (func(bool) error, error) {
	return func(bool) error { return nil }, nil
}

func killGroup(int) {}

// ProcessAlive is not read on Windows: no run is adopted there.
func ProcessAlive(int) bool { return false }
