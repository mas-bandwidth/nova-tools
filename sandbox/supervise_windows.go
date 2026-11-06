//go:build windows

package main

import (
	"io"
	"os"
	"os/exec"
)

// supervise is the Windows stub. Setpgid and Kill are not in this syscall, and
// this platform has no wall (wall_other.go). The child is waited on, nothing more.
func supervise(args []string, env []string, stderr io.Writer) int {
	self, err := os.Executable()
	if err != nil {
		return report(stderr, "", []refusal{refuse("sandbox_failed", "this binary could not be found: %v", err)})
	}
	cmd := exec.Command(self, append([]string{applyVerb}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Env = os.Stdin, os.Stdout, os.Stderr, env
	err = cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	if err != nil {
		return report(stderr, "", []refusal{refuse("sandbox_failed", "the command could not be started: %v", err)})
	}
	return 0
}
