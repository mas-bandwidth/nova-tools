package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// engine is the container runtime as this tool uses it. The podman type below
// is the real one; the tests give a fake that records every argv.
type engine interface {
	// Output runs one command to completion, bounded by ctx, and returns its
	// standard output. A non-zero exit is an error carrying its stderr.
	Output(ctx context.Context, args ...string) (string, error)
	// Start starts one command with its output streamed as it comes, in a
	// process group of its own so a terminal's interrupt reaches this tool,
	// which then removes the container, and never the runtime's client alone.
	Start(args []string, stdout, stderr io.Writer) (process, error)
}

// process is a started runtime command.
type process interface {
	// Wait returns the command's exit code; -1 when it was ended by a signal.
	Wait() (int, error)
	// Kill ends the command (only ever the client this tool started).
	Kill() error
}

type podman struct {
	bin string
	env []string
}

func newPodman(bin string, _ io.Writer) *podman {
	return &podman{bin: bin, env: runtimeEnv(os.Environ())}
}

// runtimeEnv is the environment the runtime's client is started with: this
// process's, less RUNNER_TRACKING_ID, the variable a CI runner's end-of-job
// sweep kills processes by, so that sweep never takes the runtime's monitor of
// a container that must live on to its own bound. The container itself gets
// none of this environment: only the variables its argv names.
func runtimeEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		if strings.HasPrefix(kv, "RUNNER_TRACKING_ID=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func (p *podman) Output(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, p.bin, args...)
	cmd.Env = p.env
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("%s %s: %s", p.bin, firstWords(args, 2), msg)
	}
	return out.String(), nil
}

func (p *podman) Start(args []string, stdout, stderr io.Writer) (process, error) {
	cmd := exec.Command(p.bin, args...)
	cmd.Env = p.env
	cmd.Stdin = nil
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s %s: %v", p.bin, firstWords(args, 2), err)
	}
	return &started{cmd: cmd}, nil
}

type started struct{ cmd *exec.Cmd }

func (s *started) Wait() (int, error) {
	err := s.cmd.Wait()
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return -1, err
}

func (s *started) Kill() error { return s.cmd.Process.Kill() }

func firstWords(args []string, n int) string {
	if len(args) < n {
		n = len(args)
	}
	return strings.Join(args[:n], " ")
}
