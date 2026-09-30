package darwincheck

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Spec is one process to run.
type Spec struct {
	Name string
	Args []string
	Dir  string
	// Env is the whole environment of the process; nil means the caller's own.
	Env []string
}

// Result is what a process returned. Err is set only when it could not be
// started or was cut off at the deadline; a process that ran and exited
// non-zero has Err nil and its status in Code.
type Result struct {
	Stdout, Stderr string
	Code           int
	Err            error
}

// Combined is the output of `cmd 2>&1`: standard output then standard error.
func (r Result) Combined() string { return r.Stdout + r.Stderr }

// Process is a background process the check started: a socket listener.
type Process interface {
	// Stop ends the process and reaps it.
	Stop()
}

// System is every question the check puts to the machine and every process it
// starts. The real one is OSSystem; a test hands in a System whose processes
// answer from a script, so the check's logic (what it runs, in what environment,
// what counts as a pass) is held without a wall.
type System interface {
	GOOS() string
	Environ() []string
	// LookPath is `command -v`.
	LookPath(name string) (string, bool)
	// Run starts a process and waits for it, bounded by a deadline.
	Run(s Spec) Result
	// Start starts a process in the background with its output discarded.
	Start(s Spec) (Process, error)
	// IsSocket reports whether a unix-domain socket exists at path.
	IsSocket(path string) bool
	// Sleep waits; the socket wait polls with it, and a test hands in a clock
	// that does not wait.
	Sleep(d time.Duration)

	fsProbe
}

// runDeadline bounds every process the check runs, so a wall that hangs a
// command is a FAIL and never a hung check.
const runDeadline = 2 * time.Minute

// OSSystem is the machine the check is running on.
type OSSystem struct{}

func (OSSystem) GOOS() string          { return runtime.GOOS }
func (OSSystem) Environ() []string     { return os.Environ() }
func (OSSystem) Sleep(d time.Duration) { time.Sleep(d) }

func (OSSystem) LookPath(name string) (string, bool) {
	p, err := exec.LookPath(name)
	return p, err == nil
}

func (OSSystem) IsSocket(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode()&os.ModeSocket != 0
}

func (OSSystem) IsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func (OSSystem) Readlink(path string) (string, bool) {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return "", false
	}
	t, err := os.Readlink(path)
	return t, err == nil
}

func (OSSystem) Real(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}

func (OSSystem) Run(s Spec) Result {
	ctx, cancel := context.WithTimeout(context.Background(), runDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.Name, s.Args...)
	cmd.Dir, cmd.Env = s.Dir, s.Env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	res := Result{Stdout: out.String(), Stderr: errb.String()}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case ctx.Err() != nil:
		res.Err = ctx.Err()
	case errors.As(err, &ee):
		res.Code = ee.ExitCode()
	default:
		res.Err = err
	}
	return res
}

type osProcess struct{ cmd *exec.Cmd }

func (p osProcess) Stop() {
	_ = p.cmd.Process.Kill()
	_ = p.cmd.Wait()
}

func (OSSystem) Start(s Spec) (Process, error) {
	cmd := exec.Command(s.Name, s.Args...)
	cmd.Dir, cmd.Env = s.Dir, s.Env
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return osProcess{cmd}, nil
}
