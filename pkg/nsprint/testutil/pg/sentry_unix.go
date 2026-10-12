//go:build unix

package pg

// The sentry: a postmaster outlives the test binary that started it when the
// binary dies without its cleanups (a timeout, os.Exit, a panic off the test
// goroutine, SIGKILL). The design is pkg/testredis/sentry.go: the first
// StartServer runs a second copy of the test binary as its sentry, the sentry
// leads a process group of its own and reads a pipe this binary holds, the
// postmaster joins that group as it is forked, and when this binary is gone
// the kernel closes the pipe and the sentry kills the group.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
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

// join puts a postmaster in the sentry's process group. The child does it
// between fork and exec, so there is no moment at which the server runs
// outside the group, and a group that is gone fails the start.
func join(cmd *exec.Cmd, group int) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: group}
}

// enlist starts a sentry and returns once it has said it stands.
func enlist() (*post, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("the sentry is a copy of this test binary, which was not found: %w", err)
	}
	// A long-lived child: the sentry stands for the life of the test binary,
	// under a cancellable context and no deadline, released when it is gone.
	ctx, release := context.WithCancel(context.Background())
	cmd := subproc.Long(ctx, exe, "-test.run=^$")
	cmd.Env = []string{sentryEnv + "=1"}
	// A group of its own, led by the sentry: its id is the sentry's pid.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var said bytes.Buffer
	cmd.Stderr = &said
	in, noIn := cmd.StdinPipe()
	out, noOut := cmd.StdoutPipe()
	if err := errors.Join(noIn, noOut, cmd.Start()); err != nil {
		release()
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
	bound := time.NewTimer(sentryWait)
	defer bound.Stop()
	select {
	case err = <-stands:
	case <-bound.C:
		err = fmt.Errorf("it said nothing in %v", sentryWait)
	}
	if err != nil {
		// Killed and waited for: a sentry that does not stand is not left
		// behind either. The wait closes the pipe the reader is on.
		// ignored: a sentry that did not stand is killed; the stand error is the one returned
		_ = cmd.Process.Kill()
		// ignored: the sentry was killed on the line above; its exit status is that kill
		_ = cmd.Wait()
		release()
		return nil, fmt.Errorf("the sentry %s does not stand: %w\n%s", exe, err, said.Bytes())
	}
	gone := make(chan struct{})
	go func() {
		// ignored: the sentry's end is the signal the gone channel carries; its exit status says nothing more
		_ = cmd.Wait()
		release()
		close(gone)
	}()
	return &post{group: cmd.Process.Pid, gone: gone, hold: in}, nil
}
