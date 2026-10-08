//go:build linux || darwin

package pg

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

const watchEnv = "NOVA_TEST_PG_WATCH"
const watchReady = "pg watch ready\n"

// The watcher is a copy of the test binary, with no test running. Its stdin
// belongs only to the parent. On parent death it terminates its own group,
// including the foreground postgres and its workers.
func init() {
	if os.Getenv(watchEnv) != "1" {
		return
	}
	if _, err := io.WriteString(os.Stdout, watchReady); err != nil {
		os.Exit(2)
	}
	// ignored: EOF and a broken parent pipe both mean the test binary is gone.
	_, _ = io.Copy(io.Discard, os.Stdin)
	if err := syscall.Kill(-os.Getpid(), syscall.SIGTERM); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

type watchdog struct {
	group int
	in    io.WriteCloser
	done  <-chan struct{}
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
	go func() {
		// ignored: SIGTERM is the expected watcher exit; done is the process-reap proof.
		_ = cmd.Wait()
		release()
		close(done)
	}()
	return &watchdog{group: cmd.Process.Pid, in: in, done: done}, nil
}

func (w *watchdog) alive() error {
	select {
	case <-w.done:
		return fmt.Errorf("postgres watcher ended before server launch")
	default:
		return nil
	}
}

func (w *watchdog) close() {
	if w == nil {
		return
	}
	// ignored: closing an already-broken parent pipe still wakes the watcher.
	_ = w.in.Close()
	<-w.done
}

func joinWatchdog(cmd *exec.Cmd, group int) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: group}
}
