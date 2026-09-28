//go:build unix

package testredis

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// init is the sentry. A copy of the test binary started with the mark never
// reaches the tests: it stands until its standard input closes, and kills its
// process group.
func init() {
	if os.Getenv(sentryEnv) == "1" {
		os.Exit(stand(os.Stdin, os.Stdout, killGroup))
	}
}

// killGroup kills every process in this process's group, this one included.
func killGroup() error { return syscall.Kill(0, syscall.SIGKILL) }

// join puts a server in the sentry's process group. The child does it between
// fork and exec, so there is no moment at which the server runs outside the
// group, and a group that is gone fails the start.
func join(cmd *exec.Cmd, group int) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: group}
}

// enlist starts a sentry and returns once it has said it stands.
func enlist(spec sentrySpec) (*post, error) {
	exe, err := spec.exe()
	if err != nil {
		return nil, fmt.Errorf("the sentry is a copy of this test binary, which was not found: %w", err)
	}
	cmd := exec.Command(exe, spec.args...)
	cmd.Env = spec.env
	// A group of its own, led by the sentry: its id is the sentry's pid.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var said bytes.Buffer
	cmd.Stderr = &said
	in, noIn := cmd.StdinPipe()
	out, noOut := cmd.StdoutPipe()
	if err := errors.Join(noIn, noOut, cmd.Start()); err != nil {
		return nil, fmt.Errorf("the sentry %s did not start: %w", exe, err)
	}
	stands := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(out).ReadString('\n')
		if err == nil && line != sentryStands {
			err = fmt.Errorf("it said %q", line)
		}
		stands <- err
	}()
	bound := time.NewTimer(spec.wait)
	defer bound.Stop()
	select {
	case err = <-stands:
	case <-bound.C:
		err = fmt.Errorf("it said nothing in %v", spec.wait)
	}
	if err != nil {
		// Killed and waited for: a sentry that does not stand is not left
		// behind either. The wait closes the pipe the reader is on.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("the sentry %s does not stand: %w\n%s", exe, err, said.Bytes())
	}
	gone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(gone)
	}()
	return &post{group: cmd.Process.Pid, gone: gone, hold: in}, nil
}
