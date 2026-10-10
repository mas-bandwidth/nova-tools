package functionalrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// The two container runtimes this package drives, through their command lines
// and never a client library (docs/SPEC-CI.md, "functional-container").
const (
	kindPodman = "podman"
	kindDocker = "docker"
)

// engine is the container runtime as this tool uses it. The cli type below
// is the real one; the tests give a fake that records every argv.
type engine interface {
	// Kind is the runtime's name, kindPodman or kindDocker: the argv differs
	// where docker has no equivalent flag (see testArgs and removeArgsFor).
	Kind() string
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

type cli struct {
	bin  string
	kind string
	env  []string
}

func newEngine(bin, kind string) *cli {
	return &cli{bin: bin, kind: kind, env: runtimeEnv(os.Environ())}
}

func (p *cli) Kind() string { return p.kind }

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

func (p *cli) Output(ctx context.Context, args ...string) (string, error) {
	cmd := subproc.Context(ctx, p.bin, args...)
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

func (p *cli) Start(args []string, stdout, stderr io.Writer) (process, error) {
	// A long-lived child (the container run): a cancellable context and no deadline,
	// released when the wait returns; the run's own deadline kills it through Kill.
	ctx, release := context.WithCancel(context.Background())
	cmd := subproc.Long(ctx, p.bin, args...)
	cmd.Env = p.env
	cmd.Stdin = nil
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	setOwnProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		release()
		return nil, fmt.Errorf("%s %s: %v", p.bin, firstWords(args, 2), err)
	}
	return &started{cmd: cmd, release: release}, nil
}

type started struct {
	cmd     *exec.Cmd
	release context.CancelFunc
}

func (s *started) Wait() (int, error) {
	err := s.cmd.Wait()
	s.release()
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
	n = min(n, len(args))
	return strings.Join(args[:n], " ")
}

// runtimeNames are the container runtimes in the order `auto` prefers them:
// the fleet's runtime is podman (docs/FLEET.md); docker is the fallback for a
// machine that has only it.
var runtimeNames = []string{kindPodman, kindDocker}

// errNoRuntime is what a machine with no container runtime gets: the tests
// never run bare (docs/SPEC-CI.md, "functional-container").
var errNoRuntime = errors.New("no container runtime on PATH: install podman (docs/FLEET.md; the fleet's container-runtime play does it) or docker")

// kindOf names the runtime of a binary by its file name: docker for a name
// holding docker, podman otherwise.
func kindOf(bin string) string {
	if strings.Contains(strings.ToLower(filepath.Base(bin)), kindDocker) {
		return kindDocker
	}
	return kindPodman
}

// chooseRuntime is the binary to run and the name to say it by. An explicit
// binary path (--podman) is used as given, not looked up. Otherwise want is
// auto (podman first, docker second), podman or docker, looked up on PATH; a
// runtime that is not there is an error, never a bare run.
func chooseRuntime(explicit, want string, lookPath func(string) (string, error)) (bin, name string, err error) {
	if explicit != "" {
		return explicit, kindOf(explicit), nil
	}
	names := runtimeNames
	switch want {
	case "", "auto":
	case kindPodman, kindDocker:
		names = []string{want}
	default:
		return "", "", fmt.Errorf("--runtime %q is not podman, docker or auto", want)
	}
	for _, n := range names {
		if p, lerr := lookPath(n); lerr == nil {
			return p, n, nil
		}
	}
	if len(names) == 1 {
		return "", "", fmt.Errorf("--runtime %s: %w (not on PATH)", want, errNoRuntime)
	}
	return "", "", errNoRuntime
}

// useRuntime picks the runtime and names it on stderr, so a run's log says
// which one ran the container; false when there is none.
func useRuntime(explicit, want string, lookPath func(string) (string, error), stderr io.Writer) (string, string, bool) {
	bin, name, err := chooseRuntime(explicit, want, lookPath)
	if err != nil {
		if errors.Is(err, errNoRuntime) {
			fmt.Fprintf(stderr, "CI FUNCTIONAL REFUSED reason=no_container_runtime requested=%s\n", orAuto(want))
		}
		fmt.Fprintf(stderr, "functionalrun: %v\n", err)
		return "", "", false
	}
	fmt.Fprintf(stderr, "functionalrun: container runtime: %s (%s)\n", name, bin)
	return bin, name, true
}

func orAuto(s string) string {
	if s == "" {
		return "auto"
	}
	return s
}
