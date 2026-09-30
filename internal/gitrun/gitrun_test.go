package gitrun_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

func fakeGit(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake git is a shell script")
	}
	path := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaultTimeoutIsSixtySeconds(t *testing.T) {
	t.Parallel()

	if gitrun.DefaultTimeout != 60*time.Second {
		t.Fatalf("DefaultTimeout is %s, want 60s", gitrun.DefaultTimeout)
	}
	cmd, cancel := gitrun.Command(context.Background(), gitrun.Options{C: "/somewhere"}, "status")
	defer cancel()
	if cmd.WaitDelay != subproc.WaitDelay {
		t.Fatalf("WaitDelay is %s, want %s", cmd.WaitDelay, subproc.WaitDelay)
	}
	if got := strings.Join(cmd.Args[1:], " "); got != "-C /somewhere status" {
		t.Fatalf("args are %q", got)
	}
}

func TestAFinishedGitReturnsItsStreamsAndItsExitError(t *testing.T) {
	t.Parallel()

	bin := fakeGit(t, "echo out\necho err >&2\nexit 3\n")
	res, err := gitrun.Run(context.Background(), gitrun.Options{Bin: bin}, "x")
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Fatalf("got %v", err)
	}
	if string(res.Stdout) != "out\n" || string(res.Stderr) != "err\n" {
		t.Fatalf("streams %q %q", res.Stdout, res.Stderr)
	}
	out, err := gitrun.Combined(context.Background(), gitrun.Options{Bin: bin, Env: []string{"PATH=/usr/bin:/bin"}}, "x")
	if !errors.As(err, &ee) || !strings.Contains(string(out), "out") || !strings.Contains(string(out), "err") {
		t.Fatalf("combined %q %v", out, err)
	}
}

// standing is a writer that closes ready when the child has said it stands, so the test
// acts on the event and never on the clock.
type standing struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	ready chan struct{}
	once  sync.Once
}

func (s *standing) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.buf.Write(p)
	if strings.Contains(s.buf.String(), "standing") {
		s.once.Do(func() { close(s.ready) })
	}
	return n, err
}

// A git whose child holds the pipe after git is killed: the context ending kills it, and
// WaitDelay (Options.WaitDelay, shortened for the run) frees the pipe, so Wait returns.
// Driven by an event, not the clock; the real deadline is the functional test.
func TestAKilledGitDoesNotHangOnItsPipe(t *testing.T) {
	t.Parallel()

	bin := fakeGit(t, "sleep 30 &\necho standing\nsleep 30\n")
	cmd, cancel := gitrun.Command(context.Background(), gitrun.Options{Bin: bin, WaitDelay: 100 * time.Millisecond}, "fetch")
	defer cancel()
	out := &standing{ready: make(chan struct{})}
	cmd.Stdout = out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	<-out.ready
	cancel()
	if err := cmd.Wait(); err == nil {
		t.Fatal("a git killed while it slept returned no error")
	}
}

// A caller whose own deadline has passed: the git does not run, the error is the timeout
// type, and it names no budget because the caller's deadline, not the default, applied.
func TestACallersPassedDeadlineIsATimeoutThatNamesNoBudget(t *testing.T) {
	t.Parallel()

	bin := fakeGit(t, "exit 0\n")
	passed, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer stop()
	_, err := gitrun.Run(passed, gitrun.Options{Bin: bin}, "fetch")
	var te *subproc.TimeoutError
	if !errors.As(err, &te) || te.Budget != 0 || !strings.Contains(te.Error(), "before its deadline") {
		t.Fatalf("got %v", err)
	}
}
