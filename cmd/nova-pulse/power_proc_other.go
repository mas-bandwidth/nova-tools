//go:build !unix

package main

import "os/exec"

// powerSetProcessGroup is a no-op where process groups are not a thing.
func powerSetProcessGroup(cmd *exec.Cmd) {}

// powerKillProcessGroup kills the child itself where there is no group to kill.
func powerKillProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
