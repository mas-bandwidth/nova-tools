// Command functionalrun runs the functional tier (the tests behind
// `//go:build functional`) inside ONE container per run, so every dependency a
// test starts (redis-server, postgres, a built binary) lives and dies with the
// container. The fixtures do not change: they start their dependencies as child
// processes, and here those children are inside the container.
//
// The container runtime owns the run, never this process:
//
//   - the bound is the runtime's: `podman run --timeout` is enforced by the
//     runtime's monitor outside the container, and it holds when this process
//     is killed; this process also removes the container itself when the
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
// run the container or the container was not gone afterwards.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const usage = `functionalrun: the functional tier inside one container per run (see TESTING.md)

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
      --deadline <duration>   the whole run's bound, at least 30s (default 10m)
      --grace <duration>      how long past its deadline a container is left
                              before the reaper removes it (default 30s)
      --cpus <n>              CPUs for the container, and go test -p (default 4)
      --memory <size>         memory for the container, no swap (default 4g)
      --pids <n>              the container's process limit (default 1024)
      --scratch <size>        the size of the /tmp tmpfs (default 2g)
      --gocache-volume <name> the Go build cache volume (default: this user's)
      --gomod-volume <name>   the Go module cache volume (default: this user's)
      --podman <path>         the podman binary (default: podman on PATH)

  functionalrun reap [--grace <duration>] [--dry-run] [--podman <path>]
      Remove every container carrying this tool's run label whose deadline
      label plus the grace has passed, in any state. Nothing else is touched:
      no container without the label, no cache volume, no process.

exit codes (run): the container's own (make test-functional: 0 green, 2 red);
  124 the deadline ended the run; 130 interrupted; 125 the run could not be
  started, or a container of the run was still present at the end.
exit codes (reap): 0 nothing reaped; 1 a container was reaped; 2 could not run.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	os.Exit(dispatch(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func dispatch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "run":
		cfg, err := parseRun(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "functionalrun run: %v; run: functionalrun help\n", err)
			return exitCannotRun
		}
		eng := newPodman(cfg.podman, stderr)
		return runTier(ctx, eng, cfg, realClock{}, stdout, stderr)
	case "reap":
		cfg, err := parseReap(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "functionalrun reap: %v; run: functionalrun help\n", err)
			return 2
		}
		eng := newPodman(cfg.podman, stderr)
		n, err := reap(ctx, eng, time.Now(), cfg.grace, cfg.dryRun, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "functionalrun reap: %v\n", err)
			return 2
		}
		if n > 0 {
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
