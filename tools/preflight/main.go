// Command preflight is the standard check before a card is submitted or a pull
// request is opened: gofmt, go vet, and the unit tests through `make test-full`,
// in that order, stopping at the first that fails.
//
// It prints one banner per step and ALL CHECKS PASSED at the end; a failing
// step prints what failed and the tool exits with that step's own exit code
// (gofmt findings exit 1). Nothing it runs is mocked: the toolchain it calls
// is the one named by GO, GOFMT and MAKE.
//
// Flags:
//
//	-h, --help       print the usage and exit 0
//	-run <pattern>   run only the tests whose names match the regexp
//	--run <pattern>  the same
//
// Every other argument is a package to check; with none, PKGS names the set,
// and with no PKGS the set is ./cmd/... ./internal/... ./pkg/... .
//
// Environment:
//
//	GO                 the go command (default go)
//	GOFMT              the gofmt command (default gofmt)
//	MAKE               the make command for the test step (default make)
//	PKGS               the package set when no package is given on the command line
//	RUN                the test regexp when -run is not given
//	NOVA_TEST_NO_HOST  the host guard every test runs under (default 1)
//
//	example:
//	  go run ./tools/preflight ./pkg/swarm
//	  go run ./tools/preflight -run TestLease ./pkg/swarm ./cmd/nova-swarm
//	  make preflight PKGS=./pkg/swarm RUN=TestLease
package main

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

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// Exit codes: the exit code of the step that failed, else 0. A gofmt finding,
// a bad flag and an unreadable working directory are 1.
const (
	exitOK    = 0
	exitFail  = 1
	exitNoCmd = 127 // a toolchain command that could not be started
)

// defaultPkgs is the package set when neither the command line nor PKGS names
// one.
const defaultPkgs = "./cmd/... ./internal/... ./pkg/..."

// usage is the help text.
const usage = `Usage: preflight [flags] [packages...]

Runs preflight: gofmt check, go vet, and targeted unit tests.

Flags:
  -h, --help        Show this help and exit
  -run <pattern>    Filter unit tests by regex
  --run <pattern>   Alias for -run

Environment variables:
  GO                Go command (default: go)
  GOFMT             gofmt command (default: gofmt)
  MAKE              make command for the test step (default: make)
  PKGS              Packages to test when no arguments provided
  RUN               Test regex filter
  NOVA_TEST_NO_HOST Safe test seam guard (default: 1)
`

// command is one process the tool starts.
type command struct {
	name string
	args []string
	env  []string // the whole environment of the process
	dir  string   // the working directory
	out  io.Writer
	err  io.Writer
}

// runner starts processes. The real one is execRunner; a test hands in a fake
// that records each command and answers with a scripted exit code, so the
// handoff to gofmt, go vet and make is checked without running any of them.
type runner interface {
	// run starts the command and returns its exit code. A command that could not
	// be started returns exitNoCmd and the reason as the error.
	run(c command) (int, error)
}

type execRunner struct{}

// run starts each step as a long-lived child (pkg/subproc): gofmt, go vet and
// the test run take as long as the packages take, and go test bounds itself, so
// the child has no deadline of its own, only a bounded wait for its pipes.
func (execRunner) run(c command) (int, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := subproc.Long(ctx, c.name, c.args...)
	cmd.Env = c.env
	cmd.Dir = c.dir
	cmd.Stdout = c.out
	cmd.Stderr = c.err
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return exitNoCmd, err
}

// env is everything a run reads from outside itself.
type env struct {
	stdout, stderr io.Writer
	environ        []string // the process environment, KEY=value
	dir            string   // the repository root every step runs in
	runner         runner
}

func main() {
	root, err := startRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "preflight: %v\n", err)
		os.Exit(exitFail)
	}
	os.Exit(run(os.Args[1:], env{
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		environ: os.Environ(),
		dir:     root,
		runner:  execRunner{},
	}))
}

// startRoot is the repository root above the process's working directory.
func startRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return repoRoot(wd)
}

// repoRoot is the nearest directory at or above dir that holds go.mod: the
// tree every step checks, wherever preflight was started from.
func repoRoot(dir string) (string, error) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod at or above the working directory; run preflight inside the repository")
		}
		dir = parent
	}
}

// lookup reads a variable from the environment list; the last value wins, as in
// a process environment.
func lookup(environ []string, key string) string {
	v := ""
	for _, kv := range environ {
		if k, val, ok := strings.Cut(kv, "="); ok && k == key {
			v = val
		}
	}
	return v
}

// orDefault is ${VAR:-def}: the default when the variable is unset or empty.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// run is the whole tool: parse, then the three steps in order.
func run(args []string, e env) int {
	goCmd := orDefault(lookup(e.environ, "GO"), "go")
	gofmtCmd := orDefault(lookup(e.environ, "GOFMT"), "gofmt")
	makeCmd := orDefault(lookup(e.environ, "MAKE"), "make")
	// The tests run with the host guard on unless the caller chose otherwise.
	childEnv := append(append([]string{}, e.environ...), "NOVA_TEST_NO_HOST="+orDefault(lookup(e.environ, "NOVA_TEST_NO_HOST"), "1"))

	runPattern := ""
	var targets []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-h", "--help":
			fmt.Fprint(e.stdout, usage)
			return exitOK
		case "-run", "--run":
			if i+1 >= len(args) {
				fmt.Fprintf(e.stderr, "preflight: %s requires a regex pattern argument\n", a)
				return exitFail
			}
			runPattern = args[i+1]
			i++
		default:
			targets = append(targets, a)
		}
	}

	pkgs := strings.Join(targets, " ")
	if len(targets) == 0 {
		pkgs = orDefault(lookup(e.environ, "PKGS"), defaultPkgs)
	}

	step := func(c command) (int, bool) {
		c.env, c.dir = childEnv, e.dir
		code, err := e.runner.run(c)
		if err != nil {
			fmt.Fprintf(e.stderr, "preflight: %s: %v\n", c.name, err)
		}
		return code, code == 0
	}

	// 1. gofmt.
	fmt.Fprintln(e.stdout, "=== [1/3] Preflight: gofmt check ===")
	var listed bytes.Buffer
	// A gofmt that cannot run lists nothing, as a silent failure of the listing
	// always has; the vet step that follows fails on the same tree.
	step(command{name: gofmtCmd, args: []string{"-l", "."}, out: &listed, err: io.Discard})
	var unformatted []string
	for _, l := range strings.Split(listed.String(), "\n") {
		if l != "" {
			unformatted = append(unformatted, l)
		}
	}
	if len(unformatted) > 0 {
		fmt.Fprintln(e.stderr, "preflight: gofmt check FAILED: unformatted files detected:")
		fmt.Fprintln(e.stderr, strings.Join(unformatted, "\n"))
		return exitFail
	}
	fmt.Fprintln(e.stdout, "gofmt: OK")

	// 2. go vet, over the package set split on whitespace as a shell would.
	fmt.Fprintf(e.stdout, "=== [2/3] Preflight: go vet (%s) ===\n", pkgs)
	if code, ok := step(command{name: goCmd, args: append([]string{"vet"}, strings.Fields(pkgs)...), out: e.stdout, err: e.stderr}); !ok {
		return code
	}
	fmt.Fprintln(e.stdout, "go vet: OK")

	// 3. the tests, through the Makefile's test-full. GO is forwarded so the
	// target runs the toolchain vet just used; PKGS travels as one argument and
	// the Makefile splits it.
	fmt.Fprintf(e.stdout, "=== [3/3] Preflight: targeted unit tests (%s) ===\n", pkgs)
	makeArgs := []string{"test-full", "GO=" + goCmd, "PKGS=" + pkgs}
	if re := orDefault(runPattern, lookup(e.environ, "RUN")); re != "" {
		makeArgs = append(makeArgs, "RUN="+re)
	}
	if code, ok := step(command{name: makeCmd, args: makeArgs, out: e.stdout, err: e.stderr}); !ok {
		return code
	}
	fmt.Fprintln(e.stdout, "unit tests: OK")

	fmt.Fprintln(e.stdout, "=== Preflight: ALL CHECKS PASSED ===")
	return exitOK
}
