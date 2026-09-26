// nova-ci runs the checks this repository's CI path makes on its own output.
// Its one verb, slowtests, reads the newline-delimited `go test -json`
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
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ci/functional"
	"github.com/mas-bandwidth/nova-tools/internal/ci/slowtests"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-ci: the checks this repository's CI runs on its own test output (see docs/SPEC-CI.md)

usage:
  nova-ci help        print this banner and the verbs below
  nova-ci version     which build this is: <version> <goos>/<goarch> <go version>
  nova-ci slowtests --budget <seconds>
                      read newline-delimited ` + "`go test -json`" + ` TestEvents on stdin and
                      print one CI-SLOW line per package whose total elapsed
                      time is over --budget (default 60); exit 2 when any
                      package is over, 0 when none is.
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
                      Exit 0 green, 1 a red test or build, 2 over the
                      budgets or could not run.
  nova-ci slowtests --package-budget <s> --test-budget <s> [--allowlist <file>]
                    [--sleeps <file>] [--max-load-per-cpu <n>] [--load <n> --cpus <n>]
                      the unit tier's budgets: a package over --package-budget
                      and a top-level test over --test-budget are each a CI-SLOW
                      line, unless the allowlist (pkg<TAB>test<TAB>seconds<TAB>
                      <measured>s@<where>, - in the test column for a package's
                      own row) names a higher one. With --max-load-per-cpu the
                      host's load average (the larger of its 1- and 5-minute
                      figures, over its CPUs; --load and --cpus give them by
                      hand) is printed as a CI-LOAD line, and above the gate the
                      CI-SLOW lines are printed and do not fail the run. A test
                      skipped with the SLEEPS marker and not on --sleeps
                      (pkg<TAB>test<TAB>where) is a CI-SLEEPS line and fails the
                      run at any load.
  nova-ci functional <package-dir>...
                      print the packages among these that hold functional tests
                      (a _test.go built only under the functional build tag) on
                      one line and a go test -run pattern naming exactly those
                      tests on the next; print nothing when there are none.
  nova-ci new-rule [--root <checkout>] <rule-name>
                      scaffold a new class rule skeleton: class test, fixture, and makefile
  nova-ci new-verb [--root <checkout>] <tool> <verb>
                      scaffold a new CLI verb skeleton: command, test, fixture, and makefile

exit codes: 0 inside budget, 2 a package is over budget or the invocation
            could not run (bad flag, unreadable stdin); local adds 1 for a red
            test or a package that did not build.

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

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
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
	case "new-rule":
		return cmdNewRule(args[1:], stdout, stderr)
	case "new-verb":
		return cmdNewVerb(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		return refuse(stderr, "", fmt.Sprintf("unknown subcommand %q", args[0]))
	}
}

// cmdSlowtests reads the events, sums them against the budget, and prints the
// verdict: one CI-SLOW line per over-budget package (exit 2), or the single
// CI-SLOW OK line (exit 0). A malformed line or an unusable flag is a refusal.
func cmdSlowtests(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("slowtests", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	budget := fs.Int("budget", 60, "whole seconds a package's tests may take before it is over budget")
	packageBudget := fs.Float64("package-budget", 0, "seconds a package's tests may take; replaces --budget when set")
	testBudget := fs.Float64("test-budget", 0, "seconds one top-level test may take; 0 judges packages only")
	allowlist := fs.String("allowlist", "", "pkg<TAB>test<TAB>seconds<TAB><measured>s@<where> rows that raise one package's or one test's budget")
	sleeps := fs.String("sleeps", "", "pkg<TAB>test<TAB>where rows: the tests already skipped with the SLEEPS marker")
	maxLoad := fs.Float64("max-load-per-cpu", 0, "above this load average a CPU the time budgets are measured, not enforced; 0 always enforces")
	loadFlag := fs.Float64("load", -1, "the host's load average, instead of reading it")
	cpusFlag := fs.Int("cpus", 0, "the host's logical CPUs, instead of runtime.NumCPU")
	if err := fs.Parse(args); err != nil {
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
	if *maxLoad < 0 || *cpusFlag < 0 {
		return refuse(stderr, " slowtests", "--max-load-per-cpu and --cpus must not be negative")
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
	var load slowtests.Load
	if *maxLoad > 0 {
		load = hostLoad()
		if *loadFlag >= 0 {
			load.Avg, load.Known, load.Why = *loadFlag, true, ""
		}
		if *cpusFlag > 0 {
			load.CPUs = *cpusFlag
		}
	}
	ledger := *sleeps
	if ledger == "" {
		ledger = "the SLEEPS ledger (no --sleeps given)"
	}
	lines, code := slowtests.Verdict(report, load, *maxLoad, ledger)
	for _, line := range lines {
		fmt.Fprintln(stdout, line)
	}
	return code
}

// cmdFunctional prints the functional tier's selection for `make
// test-functional`: the package directories among args that hold functional
// tests, space-separated, then one -run pattern naming exactly those tests. A
// change whose packages carry none prints nothing and exits 0, and the target
// runs nothing.
func cmdFunctional(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, " functional", "no package directory given; pass the packages the change touched (./cmd/nova-sprint ...)")
	}
	dirs, err := functional.Expand(args)
	if err != nil {
		return refuse(stderr, " functional", oneline.Err(err))
	}
	pkgs, err := functional.Select(dirs)
	if err != nil {
		return refuse(stderr, " functional", oneline.Err(err))
	}
	if len(pkgs) == 0 {
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
