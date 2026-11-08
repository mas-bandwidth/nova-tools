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
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case s := <-sigs:
				// ESRCH is a command that exited before the signal reached it.
				if err := syscall.Kill(cmd.Process.Pid, s.(syscall.Signal)); err != nil && err != syscall.ESRCH {
					fmt.Fprintf(stderr, "nova-sandbox: %v could not be forwarded to the command: %v\n", s, err)
				}
			case <-done:
				return
			}
		}
	}()
	err = cmd.Wait()
	signal.Stop(sigs)
	close(done)
	<-stopped
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
