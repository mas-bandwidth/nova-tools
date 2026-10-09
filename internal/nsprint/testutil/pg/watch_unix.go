//go:build linux || darwin

package pg

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

const watchEnv = "NOVA_TEST_PG_WATCH"
const watchReady = "pg watch ready\n"
const watchTermGrace = 300 * time.Millisecond
const watchCloseLimit = 5 * time.Second

// The watcher is a copy of the test binary, with no test running. Its stdin
// belongs only to the parent. On parent death it terminates its own group,
// including the foreground postgres and its workers.
func init() {
	if os.Getenv(watchEnv) != "1" {
		return
	}
	// The watcher leads this private group. It must survive its own TERM long
	// enough to KILL members that ignored TERM or were stopped by the kernel.
	signal.Ignore(syscall.SIGTERM)
	if _, err := io.WriteString(os.Stdout, watchReady); err != nil {
		os.Exit(2)
	}
	// ignored: EOF and a broken parent pipe both mean the test binary is gone.
	_, _ = io.Copy(io.Discard, os.Stdin)
	group := -os.Getpid() // this live group leader pins the identity through both signals
	if err := syscall.Kill(group, syscall.SIGTERM); err != nil {
		os.Exit(2)
	}
	time.Sleep(watchTermGrace)
	// SIGKILL reaches every remaining member, including this watcher. A failed
	// call exits 2; the parent never treats watcher reaping alone as group quiet.
	if err := syscall.Kill(group, syscall.SIGKILL); err != nil {
		os.Exit(2)
	}
	os.Exit(2) // unreachable when the owned group accepted SIGKILL
}

type watchdog struct {
	group int
	in    io.WriteCloser
	done  <-chan struct{}
	wait  error // written before done closes
}

func startWatchdog() (*watchdog, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	ctx, release := context.WithCancel(context.Background())
	cmd := subproc.Long(ctx, exe, "-test.run=^$")
	cmd.Env = []string{watchEnv + "=1"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	in, err := cmd.StdinPipe()
	if err != nil {
		release()
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		release()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		release()
		return nil, err
	}
	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(out).ReadString('\n')
		if err == nil && line != watchReady {
			err = fmt.Errorf("watcher said %q", line)
		}
		ready <- err
	}()
	select {
	case err = <-ready:
	case <-time.After(30 * time.Second):
		err = fmt.Errorf("watcher did not stand in 30s")
	}
	if err != nil {
		// ignored: a watcher that never stood is killed; Wait below proves it ended.
		_ = cmd.Process.Kill()
		// ignored: the watcher was just killed; the stand error is returned.
		_ = cmd.Wait()
		// ignored: the failed watcher cannot hold a parent pipe after Wait.
		_ = in.Close()
		release()
		return nil, err
	}
	done := make(chan struct{})
	w := &watchdog{group: cmd.Process.Pid, in: in, done: done}
	go func() {
		w.wait = cmd.Wait()
		release()
		close(done)
	}()
	return w, nil
}

func (w *watchdog) alive() error {
	select {
	case <-w.done:
		return fmt.Errorf("postgres watcher ended before server launch")
	default:
		return nil
	}
}

func (w *watchdog) close() error {
	if w == nil {
		return nil
	}
	// ignored: closing an already-broken parent pipe still wakes the watcher.
	_ = w.in.Close()
	select {
	case <-w.done:
	case <-time.After(watchCloseLimit):
		return fmt.Errorf("postgres watcher %d did not finish group cleanup in %s", w.group, watchCloseLimit)
	}
	var exited *exec.ExitError
	if !errors.As(w.wait, &exited) || exited.ProcessState == nil {
		return fmt.Errorf("postgres watcher %d exited without its group KILL: %v", w.group, w.wait)
	}
	status, ok := exited.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		return fmt.Errorf("postgres watcher %d did not finish its group KILL: %v", w.group, w.wait)
	}
	deadline := time.Now().Add(watchCloseLimit)
	for {
		// Probe only: after the watcher is reaped its numeric group may be reused.
		// ESRCH proves that no group member remains, including an unreaped zombie.
		err := syscall.Kill(-w.group, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			return fmt.Errorf("postgres group %d quiet check: %w", w.group, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("postgres group %d remains after watcher KILL", w.group)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func joinWatchdog(cmd *exec.Cmd, group int) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: group}
}
