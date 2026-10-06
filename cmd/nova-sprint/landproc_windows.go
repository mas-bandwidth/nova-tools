//go:build windows

package main

import "os/exec"

// Windows is a client, not a bench: there is no process group to own, so these
// are the honest no-ops -- a cancelled context ends the child alone, and the
// recorded gate is a record of nothing.
func ownLandGroup(cmd *exec.Cmd) {}

func killLandGroup(pgid int) {}

func landGroupAlive(pgid int) bool { return false }
