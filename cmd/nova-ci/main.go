// nova-ci runs the checks this repository's CI path makes on its own output.
// Its first verb, slowtests, reads the newline-delimited `go test -json`
// TestEvents on stdin, sums the package-level elapsed time for each package,
// and refuses (exit 2) every package whose total is over the budget, one line
// each. It exists because a slow test must surface the moment it happens:
// nova-secrets sat at 120 seconds unnoticed until an alarm like this one.
//
// Every path and every budget comes from a flag. There are no guessed paths; a
// budget of zero or less is refused rather than read as unlimited.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ci/functional"
	"github.com/mas-bandwidth/nova-tools/internal/ci/slowtests"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-ci: the checks this repository's CI runs on its own test output (see docs/SPEC-CI.md)

usage:
  nova-ci help        print this banner and the verbs below
  nova-ci version     which build this is: <version> <goos>/<goarch> <go version>
  nova-ci slowtests --budget <seconds>
                      read newline-delimited ` + "`go test -json`" + ` TestEvents on stdin and
                      print one CI-SLOW line per package whose total elapsed
                      time is over --budget (default 60) and a CI-LOAD line;
                      exit 0: the times are a measurement. --enforce makes a
                      CI-SLOW line exit 2 (the nightly reference leg only).
  nova-ci local [--base origin/dev] [--functional]
                      the unit tier CI runs for this diff, on this machine:
                      the packages .github/scripts/select-packages.sh picks
                      against the merge base of --base and HEAD, run through
                      the Makefile's test target (its go test flags and its
                      slowtests budgets) under nice -n 15 at -p 2, GOMAXPROCS=2
                      and -count=1; one PKG line per package with its seconds,
                      one RED line per failing test with its output.
                      --functional adds the functional build tag
                      (GOTEST_TAGS=functional); CI runs those tests in its
                      functional job as a stream merges.
                      Exit 0 green, 1 a red test or build, 2 a CI-SLEEPS
                      line or could not run.
  nova-ci slowtests --package-budget <s> --test-budget <s> [--allowlist <file>]
                    [--sleeps <file>] [--enforce] [--load <n> --cpus <n>]
                      the unit tier's budgets: a package over --package-budget
                      and a top-level test over --test-budget are each a CI-SLOW
                      line, unless the allowlist (pkg<TAB>test<TAB>seconds<TAB>
                      <measured>s@<where>, where is run<id> or a bench, - in
                      the test column for a package's own row) names a higher
                      one. The host's load average (the larger of its 1- and
                      5-minute figures, over its CPUs; --load and --cpus give
                      them by hand) is printed as a CI-LOAD line and never
                      read by the verdict. A CI-SLOW line fails the run only
                      with --enforce. A test skipped with the SLEEPS marker and
                      not on --sleeps (pkg<TAB>test<TAB>where) is a CI-SLEEPS
                      line and fails the run on every leg.
                      A package go test served from its test cache reports a
                      package elapsed near zero, so a cached run can never
                      trip --package-budget (or --budget); its tests replay
                      the times of the run that was cached, which
                      --test-budget still reads. CI's unit legs run with the
                      cache on (GOTEST_COUNT_FLAG=); its --enforce leg runs
                      -count=1, and so does a measurement by hand.
  nova-ci functional <package-dir>...
                      print the packages among these that hold functional tests
                      (a _test.go built only under the functional build tag) on
                      one line and a go test -run pattern naming exactly those
                      tests on the next; when there are none, one line
                      CI FUNCTIONAL OK packages=0 reason=<why>. A flag, and a
                      pattern matching no package, are refused (exit 2).
  nova-ci sweep-orphans [--pid-dir <dir>]
                      sweep orphaned test redis-server processes recorded by
                      testutil.Start and remove stale PID files
  nova-ci new-rule [--root <checkout>] <rule-name>
                      scaffold a new class rule skeleton: class test, fixture, and makefile
  nova-ci new-verb [--root <checkout>] <tool> <verb>
                      scaffold a new CLI verb skeleton: command, test, fixture, and makefile
  nova-ci github receipt --from-runner --redis <addr> --repo owner/name
                    --sha <40hex> --run-id <n> --workflow <name>
                    --conclusion success|failure|cancelled [--pr <n>] [--at <rfc3339>]
                      the ci-ok job's run receipt: one ev:github row of the
                      workflow_run shape, sender runner; dialled as the
                      environment's seat (NOVA_SPRINT_REDIS_USER). One CI
                      RECEIPT line;
                      exit 0 written, 1 the store refused it, 2 usage.
  nova-ci cost --repo owner/name --sha <40hex> --run-id <n> --workflow <name>
               --conclusion success|failure|cancelled [--pr <n>] [--at <rfc3339>]
               [--redis <addr>] < jobs.json
                      the one COST line of a CI run: read the run's complete
                      job listing (repos/<owner>/<name>/actions/runs/<id>/jobs:
                      one JSON object whose jobs array holds exactly total_count
                      jobs, one complete page or pages combined into one object;
                      raw concatenated pages are refused) on stdin and print
                      job-seconds per job, the total, and spin (the seconds of
                      the failed, cancelled and rerun jobs); the flags are the
                      run receipt's. --redis appends the same entry to the
                      ci:cost stream first and the line ends in its id. Exit 0
                      with the line; 1 when the store would not take the entry
                      or closing it failed (on close failure after a successful
                      write, stdout retains the line and event id, stderr
                      reports the close error, with no rerun); 2 a refusal before
                      any dial (a flag the receipt refuses, a partial or
                      count-mismatched listing, a listing that is not the
                      forge's).

exit codes: 0 inside budget or measured, 2 a CI-SLEEPS line, a CI-SLOW
            line under --enforce, or the invocation could not run (bad flag,
            unreadable stdin, partial listing); local adds 1 for a red test or
            a package that did not build, and github receipt and cost add 1 for
            a write the store refused or a close failure.

example:
  nova-ci help
  nova-ci version
  nova-ci slowtests --budget 60
`

// refuse prints this tool's one-line refusal, escaped, and names the door.
// Package flag is given no stream so an argument holding a newline cannot
// author a second line of stderr before this code runs.
func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-ci%s: %s; run: nova-ci help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	// `<verb> -h` and `help <verb>` print that verb's help on stdout at exit 0,
	// before anything is read, run or written (the CLI style's rule (b), #4505).
	defer verbflag.Recover(stdout, "nova-ci", usage, &code)
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; the verb is slowtests (a package over its time budget)")
	}
	switch args[0] {
	case "version", "--version":
		return cmdVersion(args[1:], stdout, stderr)
	case "slowtests":
		return cmdSlowtests(args[1:], stdin, stdout, stderr)
	case "local":
		return cmdLocal(args[1:], stdout, stderr, execLocal)
	case "functional":
		return cmdFunctional(args[1:], stdout, stderr)
	case "sweep-orphans":
		return cmdSweepOrphans(args[1:], stdout, stderr)
	case "new-rule":
		return cmdNewRule(args[1:], stdout, stderr)
	case "new-verb":
		return cmdNewVerb(args[1:], stdout, stderr)
	case "github":
		return cmdGitHub(args[1:], stdout, stderr, os.Getenv)
	case "cost":
		return cmdCost(args[1:], stdin, stdout, stderr, openCostStore)
	case "help", "-h", "--help":
		if args[0] == "help" && len(args) > 1 && args[1] != "help" && !verbflag.IsHelp(args[1]) {
			return run(append(args[1:], "--help"), stdin, stdout, stderr)
		}
		fmt.Fprint(stdout, usage)
		return 0
	default:
		return refuse(stderr, "", fmt.Sprintf("unknown subcommand %q", args[0]))
	}
}

// cmdSlowtests reads the events, sums them against the budgets, and prints one
// CI-SLOW line per package or test over its budget (or the single CI-SLOW OK
// line), one CI-SLEEPS line per unledgered SLEEPS skip, and the CI-LOAD line.
// Exit 2 on a CI-SLEEPS line on every leg, on a CI-SLOW line only with
// --enforce; 0 otherwise. A malformed line or an unusable flag is a refusal.
func cmdSlowtests(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("slowtests", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	budget := fs.Int("budget", 60, "whole seconds a package's tests may take before it is over budget")
	packageBudget := fs.Float64("package-budget", 0, "seconds a package's tests may take; replaces --budget when set")
	testBudget := fs.Float64("test-budget", 0, "seconds one top-level test may take; 0 judges packages only")
	allowlist := fs.String("allowlist", "", "pkg<TAB>test<TAB>seconds<TAB><measured>s@<where> rows that raise one package's or one test's budget")
	sleeps := fs.String("sleeps", "", "pkg<TAB>test<TAB>where rows: the tests already skipped with the SLEEPS marker")
	enforce := fs.Bool("enforce", false, "fail the run on a CI-SLOW line (the nightly reference leg only); without it the times are printed and only a CI-SLEEPS line fails")
	loadFlag := fs.Float64("load", -1, "the host's load average, instead of reading it")
	cpusFlag := fs.Int("cpus", 0, "the host's logical CPUs, instead of runtime.NumCPU")
	if err := verbflag.Parse(fs, args); err != nil {
		return refuse(stderr, " slowtests", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " slowtests", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *budget <= 0 {
		return refuse(stderr, " slowtests", fmt.Sprintf("--budget must be a whole number of seconds greater than zero (got %d)", *budget))
	}

	if *packageBudget < 0 || *testBudget < 0 {
		return refuse(stderr, " slowtests", "--package-budget and --test-budget must be seconds greater than zero")
	}
	if *cpusFlag < 0 {
		return refuse(stderr, " slowtests", "--cpus must not be negative")
	}
	budgets := slowtests.Budgets{Package: float64(*budget), Test: *testBudget}
	if *packageBudget > 0 {
		budgets.Package = *packageBudget
	}
	if *allowlist != "" {
		f, err := os.Open(*allowlist)
		if err != nil {
			return refuse(stderr, " slowtests", fmt.Sprintf("--allowlist: %s", oneline.Err(err)))
		}
		rows, err := slowtests.ParseAllowlist(f)
		_ = f.Close()
		if err != nil {
			return refuse(stderr, " slowtests", fmt.Sprintf("--allowlist %s: %s", *allowlist, oneline.Err(err)))
		}
		budgets.Rows = rows
	}
	if *sleeps != "" {
		f, err := os.Open(*sleeps)
		if err != nil {
			return refuse(stderr, " slowtests", fmt.Sprintf("--sleeps: %s", oneline.Err(err)))
		}
		rows, err := slowtests.ParseSleeps(f)
		_ = f.Close()
		if err != nil {
			return refuse(stderr, " slowtests", fmt.Sprintf("--sleeps %s: %s", *sleeps, oneline.Err(err)))
		}
		budgets.Sleeps = rows
	}

	events, err := slowtests.Parse(stdin)
	if err != nil {
		return refuse(stderr, " slowtests", fmt.Sprintf("stdin is not newline-delimited go test -json: %s", oneline.Err(err)))
	}
	report := slowtests.Judge(events, budgets)
	// The load is printed, never judged: read from the host unless --load
	// gives it, so a test hands in the figure instead of reading a machine.
	load := slowtests.Load{Avg: *loadFlag, CPUs: runtime.NumCPU(), Known: true}
	if *loadFlag < 0 {
		load = hostLoad()
	}
	if *cpusFlag > 0 {
		load.CPUs = *cpusFlag
	}
	ledger := *sleeps
	if ledger == "" {
		ledger = "the SLEEPS ledger (no --sleeps given)"
	}
	lines, code := slowtests.Verdict(report, load, *enforce, ledger)
	for _, line := range lines {
		fmt.Fprintln(stdout, line)
	}
	return code
}

// cmdFunctional prints the functional tier's selection for `make
// test-functional`: the package directories among args that hold functional
// tests, space-separated, then one -run pattern naming exactly those tests.
// A change whose packages carry none prints one `CI FUNCTIONAL OK packages=0
// reason=<why>` line and exits 0, and the target runs nothing. An unknown flag
// and a pattern that matches no package are refused, every one in one line: a
// typo in CI's package list must never skip the functional tier in silence.
func cmdFunctional(args []string, stdout, stderr io.Writer) int {
	// -h and --help are the verb's help on stdout at exit 0, never silence and never a
	// package list: make test-functional would hand the help text to go test, which
	// fails on it out loud.
	verbflag.HelpIfAsked(args, "functional")
	if len(args) == 0 {
		return refuse(stderr, " functional", "no package directory given; pass the packages the change touched (./cmd/nova-table ...)")
	}
	var problems, patterns []string
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "-"):
			problems = append(problems, fmt.Sprintf("unknown flag %q (functional takes no flags, only package directories such as ./cmd/nova-table or ./internal/...)", arg))
		default:
			patterns = append(patterns, arg)
		}
	}
	problems = append(problems, functional.Unmatched(patterns)...)
	if len(problems) > 0 {
		return refuse(stderr, " functional", strings.Join(problems, "; "))
	}
	dirs, err := functional.Expand(patterns)
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
	for _, p := range pkgs {
		dirs = append(dirs, p.Dir)
	}
	fmt.Fprintln(stdout, strings.Join(dirs, " "))
	fmt.Fprintln(stdout, functional.RunPattern(pkgs))
	return 0
}
