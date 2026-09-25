// nova-ci runs the checks this repository's CI path makes on its own output.
// Its first verb, slowtests, reads the newline-delimited `go test -json`
// TestEvents on stdin, sums the package-level elapsed time for each package,
// and refuses (exit 2) every package whose total is over the budget, one line
// each. It exists because a slow test must surface the moment it happens:
// nova-secrets sat at 120 seconds unnoticed until an alarm like this one.
//
// Its second verb, failed, reads the other end of the same run: it asks a forge
// for the failing jobs of one run and prints the failing tests -- job, package,
// test, file and line, then the test's own words -- so a coordinator reads eight
// lines instead of four megabytes of log.
//
// Every path and every budget comes from a flag. There are no guessed paths; a
// budget of zero or less is refused rather than read as unlimited.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

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
  nova-ci failed --repo <owner/name> (--run <id> | --pr <n> | --branch <name>)
                      read the failing jobs of one run through gh and print the
                      failing tests -- job, package, test, file and line, then
                      the test's own words -- instead of the whole log; exit 1
                      when the run said anything red, 0 when it said nothing.
                      Run ` + "`nova-ci failed --help`" + ` for its flags.

exit codes: 0 inside budget and nothing red, 1 failed found something red,
            2 a package is over budget or the invocation could not run (bad
            flag, unreadable stdin, gh could not answer).

example:
  nova-ci help
  nova-ci version
  nova-ci slowtests --budget 60
  nova-ci failed --help
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
		return refuse(stderr, "", "no verb given; the verbs are slowtests (a package over its time budget) and failed (a run's failing tests)")
	}
	switch args[0] {
	case "version", "--version":
		return cmdVersion(args[1:], stdout, stderr)
	case "slowtests":
		return cmdSlowtests(args[1:], stdin, stdout, stderr)
	case "failed":
		return cmdFailed(args[1:], stdout, stderr, ghForge)
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
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " slowtests", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " slowtests", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *budget <= 0 {
		return refuse(stderr, " slowtests", fmt.Sprintf("--budget must be a whole number of seconds greater than zero (got %d)", *budget))
	}

	events, err := slowtests.Parse(stdin)
	if err != nil {
		return refuse(stderr, " slowtests", fmt.Sprintf("stdin is not newline-delimited go test -json: %s", oneline.Err(err)))
	}
	report := slowtests.Sum(events, time.Duration(*budget)*time.Second)
	if report.ExitCode() == 0 {
		fmt.Fprintln(stdout, report.OKLine())
		return 0
	}
	for _, line := range report.OverLines() {
		fmt.Fprintln(stdout, line)
	}
	return 2
}
