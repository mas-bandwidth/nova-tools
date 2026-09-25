//go:build windows

package main

import (
	"os"
	"os/exec"
)

// Windows is a client, not a bench: there is no process group to own and no manager's TERM
// to answer, so these are the honest no-ops -- the deadline alone ends the run.
func ownChildGroup(cmd *exec.Cmd) {}

func nativeTermCh() chan os.Signal { return make(chan os.Signal) }

func stopNativeTerm(ch chan os.Signal) {}
