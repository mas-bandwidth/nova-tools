// functional.go holds the functional verb: its flags, its run and the helpers only it uses.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/ci/functional"
	"github.com/mas-bandwidth/nova-tools/internal/ci/functionalrun"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// containerFlags are the flags of --in-container, in the order the help lists
// them. The ones that take a value are in containerValueFlags; --in-container
// and --fresh-gocache are switches. Each is forwarded as given to
// functionalrun.Prepare, which owns their validation (docs/SPEC-CI.md,
// "functional-container").
var (
	containerSwitches   = []string{"--in-container", "--fresh-gocache"}
	containerValueFlags = []string{"--runtime", "--deadline", "--memory", "--pids", "--cpus"}
	containerHelpFlags  = []verbflag.Flag{
		{Name: "in-container", Bool: true, Wants: "run the selection in ONE container (podman or docker), never bare"},
		{Name: "runtime", Wants: "podman, docker or auto (default auto: podman first, docker second)"},
		{Name: "deadline", Wants: "the container's bound, at least 30s (default 10m)"},
		{Name: "memory", Wants: "the container's memory, no swap (default 4g)"},
		{Name: "pids", Wants: "the container's process limit (default 1024)"},
		{Name: "cpus", Wants: "CPUs for the container, and go test -p (default 4)"},
		{Name: "fresh-gocache", Bool: true, Wants: "a throwaway build cache for this run only"},
	}
)

// functionalEnv is what the verb reads from the machine it runs on: the
// directory its package arguments and its source tree are relative to ("." is
// the current directory), the lookup of the container runtime's binary, and the
// context that ends the run. The tests of the verb give their own, so none
// changes the process's directory or PATH.
type functionalEnv struct {
	root string
	look func(string) (string, error)
	// open makes the runtime's engine for the binary found; nil is the real command line.
	open func(bin, kind string) functionalrun.Engine
	ctx  context.Context
}

// cmdFunctional prints the functional tier's selection for `make
// test-functional`: the package directories among args that hold functional
// tests, all on one line, then one -run pattern naming exactly those tests.
// A change whose packages carry none prints one `CI FUNCTIONAL OK packages=0
// reason=<why>` line and exits 0, and the target runs nothing. An unknown flag
// and a pattern that matches no package are refused, every one in one line: a
// typo in CI's package list must never skip the functional tier in silence.
//
// With --in-container it then runs exactly that selection in ONE container on
// podman or docker (internal/ci/functionalrun; docs/SPEC-CI.md,
// "functional-container"): stdout is the tests' own, the selection is named on
// stderr, and the exit code is the run's (0 green, 2 a red test or build, 124
// the deadline, 130 an interrupt, 125 no container runtime or a container left).
func cmdFunctional(args []string, stdout, stderr io.Writer) int {
	return functionalIn(functionalEnv{root: ".", look: exec.LookPath}, args, stdout, stderr)
}

func functionalIn(env functionalEnv, args []string, stdout, stderr io.Writer) int {
	// -h and --help are the verb's help on stdout at exit 0, never silence and never a
	// package list: make test-functional would hand the help text to go test, which
	// fails on it out loud.
	verbflag.HelpIfAsked(args, "functional", containerHelpFlags...)
	if len(args) == 0 {
		return refuse(stderr, " functional", "no package directory given; pass the packages the change touched (./cmd/nova-table ...)")
	}
	var problems, patterns, forwarded []string
	inContainer := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, _, hasValue := strings.Cut(arg, "=")
		switch {
		case arg == "--in-container":
			inContainer = true
		case arg == "--fresh-gocache":
			forwarded = append(forwarded, arg)
		case slices.Contains(containerValueFlags, name):
			if !hasValue {
				if i+1 >= len(args) {
					problems = append(problems, fmt.Sprintf("%s wants a value", name))
					continue
				}
				i++
				arg += "=" + args[i]
			}
			forwarded = append(forwarded, arg)
		case strings.HasPrefix(arg, "-"):
			problems = append(problems, fmt.Sprintf("unknown flag %q (functional takes --in-container and its flags %s, then package directories such as ./cmd/nova-table or ./internal/...)", arg, strings.Join(append(append([]string{}, containerValueFlags...), containerSwitches[1:]...), " ")))
		default:
			patterns = append(patterns, arg)
		}
	}
	if len(forwarded) > 0 && !inContainer {
		problems = append(problems, "the container flags "+strings.Join(forwarded, " ")+" mean nothing without --in-container")
	}
	at := func(p string) string { return p }
	if env.root != "." {
		at = func(p string) string { return filepath.Join(env.root, p) }
	}
	rooted := make([]string, len(patterns))
	for i, p := range patterns {
		rooted[i] = at(p)
	}
	for _, p := range functional.Unmatched(rooted) {
		if env.root != "." {
			p = strings.ReplaceAll(p, env.root+string(filepath.Separator), "")
		}
		problems = append(problems, p)
	}
	if len(problems) > 0 {
		return refuse(stderr, " functional", strings.Join(problems, "; "))
	}
	dirs, err := functional.Expand(rooted)
	if err != nil {
		return refuse(stderr, " functional", oneline.Err(err))
	}
	pkgs, err := functional.Select(dirs)
	if err != nil {
		return refuse(stderr, " functional", oneline.Err(err))
	}
	if len(pkgs) == 0 {
		fmt.Fprintf(stdout, "CI FUNCTIONAL OK packages=0 reason=no-functional-tag-in-%d-dirs\n", len(dirs))
		return 0
	}
	dirs = make([]string, 0, len(pkgs))
	runDirs := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		dirs = append(dirs, packagePath(p.Dir))
		runDirs = append(runDirs, relativeTo(env.root, p.Dir))
	}
	if !inContainer {
		fmt.Fprintln(stdout, strings.Join(dirs, " "))
		fmt.Fprintln(stdout, functional.RunPattern(pkgs))
		return 0
	}
	if env.root != "." {
		forwarded = append(forwarded, "--src="+env.root)
	}
	plan, err := functionalrun.Prepare(append(forwarded, runDirs...))
	if err != nil {
		return refuse(stderr, " functional", oneline.Err(err))
	}
	fmt.Fprintf(stderr, "nova-ci functional: %d package(s) in one container: %s\n", len(runDirs), strings.Join(runDirs, " "))
	ctx := env.ctx
	if ctx == nil {
		ctx = interruptContext()
	}
	return plan.ExecuteOn(ctx, env.look, env.open, stdout, stderr)
}

// relativeTo is a selected directory as the caller wrote it: under a root other
// than the current directory, "./" and the path below the root.
func relativeTo(root, dir string) string {
	if root == "." {
		return dir
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return dir
	}
	return "./" + filepath.ToSlash(rel)
}

// interruptContext is cancelled by the first interrupt, terminate or hangup, so
// the run removes its container and prints its receipt; the next one has the
// default action again. A closed pipe on the output must not end the run before
// the removal either (docs/SPEC-CI.md, "functional-container").
func interruptContext() context.Context {
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	ctx, cancel := context.WithCancel(context.Background())
	ends := []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, ends...)
	go func() {
		<-sigs
		signal.Reset(ends...)
		cancel()
	}()
	return ctx
}

// packagePath returns dir in the form `go test -timeout 600s` accepts (./internal/x/).
func packagePath(dir string) string {
	d := filepath.ToSlash(filepath.Clean(dir))
	if d == "." {
		return "./"
	}
	if !strings.HasPrefix(d, "./") && !strings.HasPrefix(d, "../") && !strings.HasPrefix(d, "/") {
		d = "./" + d
	}
	if !strings.HasSuffix(d, "/") {
		d += "/"
	}
	return d
}
