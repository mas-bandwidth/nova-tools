//go:build !windows

package main

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// ownChildGroup makes the child the leader of a new process group, so the deadline and a
// TERM from outside kill the whole tree the card started, grandchildren included.
func ownChildGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// nativeTermCh is the channel that fires when the manager SIGTERMs this process, so the run
// can reap its own tree and fold the usage before it goes.
func nativeTermCh() chan os.Signal {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM)
	return ch
}

// stopNativeTerm stops the SIGTERM notification once the run has ended.
func stopNativeTerm(ch chan os.Signal) {
	signal.Stop(ch)
}
