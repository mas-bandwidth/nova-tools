//go:build !unix

package main

import "os/exec"

// configureCheckProcess has no process group to enter on this platform; the deadline
// still kills the child itself.
func configureCheckProcess(cmd *exec.Cmd) {}

// killCheckProcess kills the child. A platform with process groups overrides this in
// the unix build.
func killCheckProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// shellCommand runs a check through the platform shell.
func shellCommand(check string) (string, []string) {
	return "cmd", []string{"/c", check}
}
