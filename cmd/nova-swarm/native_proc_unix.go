//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// nativeOwnGroup makes the native child the leader of its own process group, so the
// deadline and a signal can end the harness AND everything it started with one kill
// (issue #1129). The wall runs the harness as its child: killing the wall alone left
// the harness holding the run's capture pipe open, so `cmd.Wait` never returned and a
// probe lived 2h28m past its 360s wall.
func nativeOwnGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
