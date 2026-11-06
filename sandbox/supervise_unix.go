//go:build !windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// supervise runs the hidden child and returns its status. The child stays in this
// process group, so a terminal read is not stopped. SIGINT, SIGTERM, SIGHUP and
// SIGQUIT are forwarded to the child's pid. On Linux, procAttr sets Pdeathsig so
// the command dies with the waiter. There is no Pdeathsig on other systems.
func supervise(args []string, env []string, stderr io.Writer) int {
	self, err := os.Executable()
	if err != nil {
		return report(stderr, "", []refusal{refuse("sandbox_failed", "this binary could not be found: %v", err)})
	}
	cmd := exec.Command(self, append([]string{applyVerb}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Env = os.Stdin, os.Stdout, os.Stderr, env
	cmd.SysProcAttr = procAttr()
	if err = cmd.Start(); err != nil {
		return report(stderr, "", []refusal{refuse("sandbox_failed", "the command could not be started: %v", err)})
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	done := make(chan struct{})
	go func() {
		select {
		case s := <-sigs:
			if sig, ok := s.(syscall.Signal); ok {
				syscall.Kill(cmd.Process.Pid, sig)
			}
		case <-done:
		}
	}()
	err = cmd.Wait()
	close(done)
	signal.Stop(sigs)
	if ee, ok := err.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			n := int(ws.Signal())
			fmt.Fprintln(stderr, signalText(n))
			return 128 + n
		}
		return ee.ExitCode()
	}
	if err != nil {
		return report(stderr, "", []refusal{refuse("sandbox_failed", "the command could not be started: %v", err)})
	}
	return 0
}
