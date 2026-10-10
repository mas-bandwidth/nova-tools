// Package functionalrun runs the functional tier (the tests behind
// `//go:build functional`) inside ONE container per run, on podman or docker,
// so every dependency a test starts (redis-server, postgres, a built binary)
// lives and dies with the container. The fixtures do not change: they start
// their dependencies as child processes, and here those children are inside the
// container. It is the engine behind `nova-ci functional --in-container` and
// `make test-functional-container` (tools/functionalrun), specified in
// docs/SPEC-CI.md ("functional-container") and modelled in tla/ContainerRun.tla.
//
// The container runtime owns the run, never this process:
//
//   - the bound is the runtime's: podman's `run --timeout` is enforced by the
//     runtime's monitor outside the container, and it holds when this process
//     is killed; docker has none, so its bound is the in-container `timeout -k`
//     under --init plus this process's own removal at the deadline and the
//     reaper's. Both engines are also removed by this process when the
//     deadline passes or it is interrupted;
//   - the removal is the runtime's: `--rm`, and a forced removal by this
//     process at the end whatever happened;
//   - a reaper removes every container of this tool whose deadline (plus a
//     grace) has passed. It selects by this tool's label, never by name, and
//     runs before every run and by hand (`functionalrun reap`).
//
// Output of the tests goes to stdout and stderr unchanged, and the exit code is
// the container's (make's: 0 green, 2 a red test or build), except where this
// tool ended the run: 124 the deadline, 130 an interrupt, 125 the tool could not
// run the container, there was no container runtime, or the container was not
// gone afterwards.
package functionalrun

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"
)

const usage = `functionalrun: the functional tier inside one container per run, on podman or docker (see TESTING.md)

usage:
  functionalrun run [flags] <package-dir>...
      Build the functional image (or reuse it), fill this user's module cache
      volume, then run make test-functional PKGS=<package-dir>... inside one
      container: the source tree read-only, /tmp and the home directory as
      writable tmpfs scratch, this user's Go build cache volume, no network,
      and a deadline enforced from outside the container. The container is
      removed at the end whatever happened. Containers of earlier runs past
      their deadline plus the grace are reaped first.

      --src <dir>             the source tree to test (default: the current directory)
      --context <dir>         the image build context, holding Containerfile
                              (default: <src>/infra/functional-image)
      --image <ref>           run this image instead of building one
      --deadline <duration>   the bound of the test container, from its start to
                              its end, at least 30s (default 10m); the runtime
                              enforces it with --timeout. It does not cover the
                              steps before and after, which have their own
                              bounds: the image build 30m, the module cache
                              step 5m, each runtime listing or removal 30 to
                              60s, the leftover check 30s in all
      --grace <duration>      how long past its deadline a container is left
                              before the reaper removes it (default 30s)
      --cpus <n>              CPUs for the container, and go test -p (default 4)
      --memory <size>         memory for the container, no swap (default 4g)
      --pids <n>              the container's process limit (default 1024)
      --scratch <size>        the size of the /tmp tmpfs (default 2g)
      --gocache-volume <name> the Go build cache volume (default: this user's)
      --gomod-volume <name>   the Go module cache volume (default: this user's)
      --fresh-gocache         a throwaway build cache for this run only: an
                              anonymous volume removed with the container.
                              Use it for code you do not trust: a run can
                              change what the next run of the same user reads
                              from the shared build cache
      --runtime <name>        podman, docker or auto (default auto: podman when it
                              is on PATH, docker second; neither is exit 125 and
                              the tests never run bare). docker has no
                              run --timeout: its bound is the in-container
                              timeout under --init, this tool's own removal at
                              the deadline plus 5s, and the reaper by the
                              deadline label
      --podman <path>         the container runtime binary, by path (its name
                              says which runtime it is); the one used is named
                              on stderr

  functionalrun reap [--grace <duration>] [--dry-run] [--runtime <name>] [--podman <path>]
      Remove every container of this tool and this user whose deadline label
      plus the grace has passed, in any state: the run label with a run id of
      the tool's own shape, and the owner label equal to this uid. A container
      of ours with an unreadable start or deadline label is reported and left.
      Nothing else is touched: no other container, no volume, no process.

exit codes (run): the container's own (make test-functional: 0 green, 2 red);
  124 the deadline ended the run; 130 interrupted; 125 the run could not be
  started, or a container of the run was still present at the end.
exit codes (reap): 0 nothing reaped and nothing unreadable; 1 a container was
  reaped, or one of ours was left with an unreadable label; 2 could not run.
`

// Usage is the help text of the standalone tool.
const Usage = usage

// Plan is one validated run: the flags of `functionalrun run` parsed and
// checked, nothing started. nova-ci's verb prepares one so a bad flag is its
// own refusal, before any container.
type Plan struct{ cfg runConfig }

// Prepare parses and validates the flags and packages of a run (the arguments
// of `functionalrun run`).
func Prepare(args []string) (*Plan, error) {
	cfg, err := parseRun(args)
	if err != nil {
		return nil, err
	}
	return &Plan{cfg: cfg}, nil
}

// Execute runs the plan in one container: the runtime is chosen (and a machine
// with none is exit 125 with `CI FUNCTIONAL REFUSED reason=no_container_runtime`),
// then every step of runTier. The exit code is the run's (see the package doc).
func (p *Plan) Execute(ctx context.Context, stdout, stderr io.Writer) int {
	return p.execute(ctx, exec.LookPath, nil, stdout, stderr)
}

// Engine is the container runtime's command line as a run uses it, and Process
// a started runtime command. A caller outside the package (a test of the verb)
// gives its own through ExecuteOn, so no test starts a container or a process.
type (
	Engine  = engine
	Process = process
)

// ExecuteOn is Execute with the lookup of the runtime's binary and the opening
// of its engine given: look finds podman or docker on a PATH of the caller's
// own, open makes the engine for the binary found (nil: the real command line).
func (p *Plan) ExecuteOn(ctx context.Context, look func(string) (string, error), open func(bin, kind string) Engine, stdout, stderr io.Writer) int {
	return p.execute(ctx, look, open, stdout, stderr)
}

func (p *Plan) execute(ctx context.Context, look func(string) (string, error), open func(bin, kind string) Engine, stdout, stderr io.Writer) int {
	bin, kind, ok := useRuntime(p.cfg.podman, p.cfg.runtime, look, stderr)
	if !ok {
		return exitCannotRun
	}
	cfg := p.cfg
	cfg.kind = kind
	if open == nil {
		open = func(bin, kind string) Engine { return newEngine(bin, kind) }
	}
	return runTier(ctx, open(bin, kind), cfg, stdout, stderr)
}

// Dispatch is the standalone tool: `run`, `reap` and `help`.
func Dispatch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return dispatch(ctx, args, stdout, stderr)
}

func dispatch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "run":
		plan, err := Prepare(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "functionalrun run: %v; run: functionalrun help\n", err)
			return exitCannotRun
		}
		return plan.Execute(ctx, stdout, stderr)
	case "reap":
		cfg, err := parseReap(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "functionalrun reap: %v; run: functionalrun help\n", err)
			return 2
		}
		bin, kind, ok := useRuntime(cfg.podman, cfg.runtime, exec.LookPath, stderr)
		if !ok {
			return 2
		}
		eng := newEngine(bin, kind)
		n, unreadable, err := reap(ctx, eng, time.Now(), cfg.grace, strconv.Itoa(os.Getuid()), cfg.dryRun, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "functionalrun reap: %v\n", err)
			return 2
		}
		if n > 0 || unreadable > 0 {
			return 1
		}
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "functionalrun: unknown verb %q; run: functionalrun help\n", args[0])
		return exitUsage
	}
}
