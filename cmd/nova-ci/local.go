package main

// local.go is `nova-ci local`: the unit tier CI runs for this
// diff, run on this machine before a push, so a child's answer is CI's answer.
//
// Children each invent their own way to test what they touch: a sharding
// script under `timeout 95`, a six-pass loop for t.Parallel violations, hand
// timing scripts. Several run the whole tree, which is CPU the real work
// needs, so this verb gives every child one way to run the unit tier.
//
// ONE IMPLEMENTATION, NOT A COPY. The verb owns no selection rule, no go test
// flag and no budget of its own:
//
//   - the packages are internal/pkgselect's answer against the merge base of
//     --base and HEAD, the selection CI's `test` matrix runs against the
//     event's base;
//   - the run is the Makefile's `test` target, the one entry CI's legs call:
//     its go test flags, its -timeout, its `nova-ci slowtests` budgets and
//     allowlist and its exit status are whatever that target holds today;
//   - --functional adds the functional build tag: the same `make test` with
//     GOTEST_TAGS=functional, so the redis-backed tests behind `//go:build
//     functional` build and run too (CI runs them in its functional job).
//
// What the verb adds is the machine it runs on: everything it starts runs under
// `nice -n 15` with GOMAXPROCS=2 and GOTEST_P=2 (go test -p 2, at most two
// cores, as a CI leg is held to), -count=1 so nothing is served from the test
// cache, and a private RUNNER_TEMP so two children never share the target's
// test.json. It reads the target's `go test -json` stream as it arrives and
// prints one PKG line per package with its seconds, then every red test by name
// with the tail of its own output.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ci/slowtests"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/yield"
)

// localNice is yield.Nice: one number for the copies and the local runs.
var _ = [1]struct{}{}[yield.Nice-15] // compile-time: localNice == yield.Nice

const (
	// localDefaultBase is the branch every card and stream lands on.
	localDefaultBase = "origin/dev"
	// localNice is the niceness of everything the verb starts: the tests share
	// the bench with the work they test. It is yield.Nice, the copies' own
	// niceness, and the verb steps itself down to it before it
	// starts anything (yield.ToCI), so the nice -n is belt and braces.
	localNice = "15"
	// localCores is the cores a CI unit leg may take, and so the most a local
	// run takes: go test -p, GOMAXPROCS and the Makefile's GOTEST_P.
	localCores = "2"
	// localOutputKept is how many of a red test's own output lines are printed
	// under it: enough for the assertion and its context, not the whole log.
	localOutputKept = 40
)

// localCmd is one child process the verb starts. Env is added to the inherited
// environment; Dir empty is the current directory.
type localCmd struct {
	Dir    string
	Env    []string
	Argv   []string
	Stdout io.Writer
	Stderr io.Writer
}

// localRunner starts c, waits for it and returns its exit status. The error is
// only for a command that could not be started at all; a non-zero exit is a
// status, not an error. It is the verb's one seam: the tests pass a fake that
// answers from a table, so no test starts git, bash, make or go.
type localRunner func(c localCmd) (int, error)

// execLocal is the production runner.
func execLocal(c localCmd) (int, error) {
	if len(c.Argv) == 0 {
		return -1, errors.New("empty command")
	}
	// A long-lived child: a local CI command runs as long as it runs, under a cancellable
	// context and no deadline.
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	cmd := subproc.Long(ctx, c.Argv[0], c.Argv[1:]...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdout, cmd.Stderr = c.Stdout, c.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// localCapture runs argv in dir and returns its trimmed stdout, its status and
// its trimmed stderr.
func localCapture(runner localRunner, dir string, env []string, argv ...string) (string, int, string, error) {
	var out, errb bytes.Buffer
	code, err := runner(localCmd{Dir: dir, Env: env, Argv: argv, Stdout: &out, Stderr: &errb})
	return strings.TrimSpace(out.String()), code, strings.TrimSpace(errb.String()), err
}

// localSelector is CI's package selection: the packages the change between
// base and HEAD touches in the checkout at root, as ./<dir>.
type localSelector func(root, base string) ([]string, error)

// localSelectThrough is internal/pkgselect's selection, its git and go commands
// started through the verb's own runner: niced, at two cores.
func localSelectThrough(runner localRunner) localSelector {
	return func(root, base string) ([]string, error) {
		run := func(dir string, env []string, argv ...string) (pkgselect.Result, error) {
			var out, errb bytes.Buffer
			code, err := runner(localCmd{Dir: dir, Env: append([]string{"GOMAXPROCS=" + localCores}, env...), Argv: append([]string{"nice", "-n", localNice}, argv...), Stdout: &out, Stderr: &errb})
			return pkgselect.Result{Stdout: out.String(), Stderr: errb.String(), Code: code}, err
		}
		res, err := pkgselect.Select(run, pkgselect.Options{Root: root, Base: base})
		return res.Packages, err
	}
}

// cmdLocal is `nova-ci local [--base <ref>] [--functional]`. Exit 0 is CI's
// green; 1 a red test or a package that did not build; 2 a CI-SLEEPS line (a
// SLEEPS skip off the ledger), a step that could not run, or a refusal. A
// CI-SLOW line is printed and, as on every CI leg but the nightly one, is not
// a verdict.
//
// The package selection is a parameter: main passes localSelectThrough over the
// same runner, a test passes a table.
func cmdLocal(args []string, stdout, stderr io.Writer, runner localRunner, selectPackages localSelector) int {
	fs := flag.NewFlagSet("local", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	base := fs.String("base", localDefaultBase, "the ref the change lands on; the diff is read from its merge base with HEAD")
	functional := fs.Bool("functional", false, "add the functional build tag (make test GOTEST_TAGS=functional), the tests CI's functional job runs")
	dryRun := fs.Bool("dry-run", false, "print the packages CI's selection picks and the make test line, and run no test")
	if err := verbflag.Parse(fs, args); err != nil {
		return refuse(stderr, " local", flagProblem(fs, err))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " local", fmt.Sprintf("unexpected argument %q; the packages are CI's selection, never named by hand", fs.Arg(0)))
	}
	if strings.TrimSpace(*base) == "" {
		return refuse(stderr, " local", "--base wants the ref the change lands on (origin/dev)")
	}
	// CI over work: this process and everything it starts.
	if err := yield.ToCI(); err != nil {
		return refuse(stderr, " local", "yield to CI: "+oneline.Err(err))
	}
	cores := []string{"GOMAXPROCS=" + localCores}
	nice := []string{"nice", "-n", localNice}

	root, code, errText, err := localCapture(runner, "", nil, "git", "rev-parse", "--show-toplevel")
	if err != nil || code != 0 || root == "" {
		return refuse(stderr, " local", fmt.Sprintf("not inside a git checkout (git rev-parse --show-toplevel: %s); run it from the repository you changed", localWhy(code, errText, err)))
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return refuse(stderr, " local", fmt.Sprintf("%s has no go.mod, so there is no CI selection to match; test only the packages you touched: nice -n %s go test -p %s -count=1 <packages>", oneline.Quote(root), localNice, localCores))
	}
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		return refuse(stderr, " local", fmt.Sprintf("%s has no readable Makefile, so there is no `make test` to run: %s", oneline.Quote(root), oneline.Err(err)))
	}
	if !localHasTarget(makefile, "test") {
		return refuse(stderr, " local", fmt.Sprintf("%s's Makefile has no test target, the one entry CI's unit legs call", oneline.Quote(root)))
	}
	if *functional && !bytes.Contains(makefile, []byte("GOTEST_TAGS")) {
		return refuse(stderr, " local", "--functional: this checkout's Makefile test target takes no GOTEST_TAGS (the functional tier, nova-tools#4328); run without --functional")
	}
	tags := ""
	if *functional {
		tags = "functional"
	}

	mergeBase, code, errText, err := localCapture(runner, root, nil, "git", "merge-base", *base, "HEAD")
	if err != nil || code != 0 || mergeBase == "" {
		return refuse(stderr, " local", fmt.Sprintf("no merge base between %s and HEAD (%s); fetch the base (git fetch origin dev) or pass --base <ref>", oneline.Quote(*base), localWhy(code, errText, err)))
	}

	// The selection reads committed history only, as CI's does; go test reads
	// the working tree. Say so when the two differ, rather than let a new
	// package go untested in silence.
	dirty, code, errText, err := localCapture(runner, root, nil, "git", "status", "--porcelain", "--untracked-files=all", "--", "*.go", "go.mod", "go.sum")
	if err != nil || code != 0 {
		return refuse(stderr, " local", fmt.Sprintf("git status could not read the working tree (%s)", localWhy(code, errText, err)))
	}
	if n := localCountLines(dirty); n > 0 {
		fmt.Fprintf(stderr, "nova-ci local: NOTE %d uncommitted Go file(s): go test reads them, but the selection reads only committed history, as CI does; commit them so the packages chosen are CI's\n", n)
	}

	pkgs, err := selectPackages(root, mergeBase)
	if err != nil {
		return refuse(stderr, " local", fmt.Sprintf("the package selection against %s failed (%s)", mergeBase, oneline.Cap(strings.TrimSpace(err.Error()), oneline.TailBytes)))
	}
	fmt.Fprintf(stdout, "nova-ci local: base=%s merge-base=%s packages=%d %s\n", oneline.Field(*base), localShort(mergeBase), len(pkgs), strings.Join(pkgs, " "))
	if len(pkgs) == 0 {
		// CI's own words for the same answer, and its exit.
		fmt.Fprintln(stdout, "nothing to test for this change")
		return 0
	}
	pkgArg := "PKGS=" + strings.Join(pkgs, " ")
	makeLine := fmt.Sprintf("nice -n %s make test %q GOTEST_P=%s GOTEST_COUNT_FLAG=-count=1 GOTEST_TAGS=%s (GOMAXPROCS=%s)", localNice, pkgArg, localCores, tags, localCores)
	if *dryRun {
		fmt.Fprintf(stdout, "nova-ci local: would run %s\n", makeLine)
		fmt.Fprintln(stdout, "nova-ci local: NOTE --dry-run ran no test; run it again without --dry-run to run them")
		return 0
	}

	tmpRoot := os.TempDir()
	tmp, err := os.MkdirTemp(tmpRoot, "nova-ci-local-")
	if err != nil {
		return refuse(stderr, " local", fmt.Sprintf("cannot make a private RUNNER_TEMP for make test's test.json: %s", oneline.Err(err)))
	}
	defer func() {
		if err := safepath.RemoveUnder(tmpRoot, tmp); err != nil {
			fmt.Fprintf(stderr, "nova-ci local: NOTE could not remove its private RUNNER_TEMP %s: %s\n", oneline.Field(tmp), oneline.Err(err))
		}
	}()

	fmt.Fprintf(stdout, "nova-ci local: %s\n", makeLine)
	col := &localCollector{out: stdout, pkgs: map[string]*localPkg{}}
	lines := &localLines{line: col.line}
	unitArgv := append(append([]string{}, nice...), "make", "test", pkgArg, "GOTEST_P="+localCores, "GOTEST_COUNT_FLAG=-count=1", "GOTEST_TAGS="+tags)
	unitCode, err := runner(localCmd{Dir: root, Env: append(cores, "RUNNER_TEMP="+tmp), Argv: unitArgv, Stdout: lines, Stderr: stderr})
	lines.flush()
	if err != nil {
		return refuse(stderr, " local", fmt.Sprintf("could not start make test: %s", oneline.Err(err)))
	}
	exit := col.finish(unitCode)
	fmt.Fprintf(stdout, "nova-ci local: exit=%d\n", exit)
	return exit
}

// localHasTarget reports whether a Makefile declares target as a rule at the
// start of a line (`test:` or `test: PKGS := ...`).
func localHasTarget(makefile []byte, target string) bool {
	return slices.ContainsFunc(strings.Split(string(makefile), "\n"), func(line string) bool {
		return strings.HasPrefix(line, target+":") && !strings.HasPrefix(line, target+":=")
	})
}

// localWhy renders why a step failed: the start error, else its stderr, else
// its exit status.
func localWhy(code int, errText string, err error) string {
	switch {
	case err != nil:
		return oneline.Err(err)
	case errText != "":
		return fmt.Sprintf("exit %d: %s", code, oneline.Cap(errText, oneline.TailBytes))
	default:
		return fmt.Sprintf("exit %d", code)
	}
}

func localCountLines(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

func localShort(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// localLines splits a byte stream into lines and hands each to line, the tail
// included once flush is called.
type localLines struct {
	buf  []byte
	line func([]byte)
}

func (l *localLines) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		l.line(l.buf[:i])
		l.buf = l.buf[i+1:]
	}
}

func (l *localLines) flush() {
	if len(l.buf) > 0 {
		l.line(l.buf)
		l.buf = nil
	}
}

// localPkg is one package of the run as the stream told it.
type localPkg struct {
	name    string
	seconds float64
	result  string              // pass, fail or skip; "" while it runs
	failed  []string            // the tests that failed, in the order they did
	output  map[string][]string // test ("" for the package) -> its last output lines
}

// localCollector reads make test's stdout: a `go test -json` TestEvent line is
// folded into its package, and anything else (make's own lines, slowtests'
// CI-SLOW, CI-SLEEPS and CI-LOAD lines) is printed as it came.
type localCollector struct {
	out   io.Writer
	order []string
	pkgs  map[string]*localPkg
	build []string
}

func (c *localCollector) line(b []byte) {
	if t := bytes.TrimSpace(b); len(t) > 0 && t[0] == '{' {
		var ev slowtests.Event
		if err := json.Unmarshal(t, &ev); err == nil {
			c.event(ev)
			return
		}
	}
	fmt.Fprintf(c.out, "%s\n", b)
}

func (c *localCollector) event(ev slowtests.Event) {
	if ev.Action == "build-output" {
		c.build = localKeep(c.build, strings.TrimRight(ev.Output, "\n"))
		return
	}
	if ev.Package == "" {
		return
	}
	pkg, ok := c.pkgs[ev.Package]
	if !ok {
		pkg = &localPkg{name: ev.Package, output: map[string][]string{}}
		c.pkgs[ev.Package] = pkg
		c.order = append(c.order, ev.Package)
	}
	switch ev.Action {
	case "output":
		pkg.output[ev.Test] = localKeep(pkg.output[ev.Test], strings.TrimRight(ev.Output, "\n"))
	case "fail":
		if ev.Test != "" {
			pkg.failed = append(pkg.failed, ev.Test)
			return
		}
		fallthrough
	case "pass", "skip":
		if ev.Test != "" {
			return
		}
		pkg.seconds += ev.Elapsed
		pkg.result = ev.Action
		fmt.Fprintf(c.out, "PKG %-4s %7s %s\n", localResult(ev.Action), slowtests.Seconds(ev.Elapsed), oneline.Field(ev.Package))
	}
}

// localKeep appends line and keeps the last localOutputKept lines.
func localKeep(lines []string, line string) []string {
	lines = append(lines, line)
	if len(lines) > 2*localOutputKept {
		lines = append([]string(nil), lines[len(lines)-localOutputKept:]...)
	}
	return lines
}

func localTail(lines []string) []string {
	if len(lines) > localOutputKept {
		return lines[len(lines)-localOutputKept:]
	}
	return lines
}

func localResult(action string) string {
	switch action {
	case "pass":
		return "ok"
	case "fail":
		return "FAIL"
	}
	return action
}

// finish prints the reds by name, each with the tail of its own output, and
// the one summary line, and returns the verb's exit for make test's status.
func (c *localCollector) finish(makeCode int) int {
	reds := 0
	var total float64
	for _, name := range c.order {
		pkg := c.pkgs[name]
		total += pkg.seconds
		for _, test := range pkg.failed {
			reds++
			fmt.Fprintf(c.out, "RED package=%s test=%s\n", oneline.Field(name), oneline.Field(test))
			for _, line := range localTail(pkg.output[test]) {
				fmt.Fprintf(c.out, "    %s\n", line)
			}
		}
		if pkg.result == "fail" && len(pkg.failed) == 0 {
			reds++
			fmt.Fprintf(c.out, "RED package=%s test=- (the package failed outside a test: a build error, a panic, TestMain or the -timeout)\n", oneline.Field(name))
			for _, line := range localTail(pkg.output[""]) {
				fmt.Fprintf(c.out, "    %s\n", line)
			}
		}
	}
	if len(c.build) > 0 {
		fmt.Fprintln(c.out, "RED build:")
		for _, line := range localTail(c.build) {
			fmt.Fprintf(c.out, "    %s\n", line)
		}
		if reds == 0 {
			reds++
		}
	}
	fmt.Fprintf(c.out, "nova-ci local: packages=%d seconds=%s red=%d make-exit=%d\n", len(c.order), slowtests.Seconds(total), reds, makeCode)
	switch {
	case makeCode == 0:
		return 0
	case reds > 0:
		return 1
	default:
		fmt.Fprintln(c.out, "nova-ci local: make test failed with no red test: a CI-SLEEPS line above is a SLEEPS skip off the ledger, or a step could not run (its words are above)")
		return 2
	}
}
