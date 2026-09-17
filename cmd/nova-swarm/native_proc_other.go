//go:build !unix

package main

import "os/exec"

// nativeOwnGroup has no process group to join where the platform has none; the
// platform's own kill of the direct child is the whole bound (issue #1129).
func nativeOwnGroup(cmd *exec.Cmd) {}
