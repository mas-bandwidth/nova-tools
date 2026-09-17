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
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci/slowtests"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-ci: the checks this repository's CI runs on its own test output (see docs/SPEC-CI.md)

usage:
  nova-ci help        print this banner and the verbs below
  nova-ci slowtests --budget <seconds>
                      read newline-delimited ` + "`go test -json`" + ` TestEvents on stdin and
                      print one CI-SLOW line per package whose total elapsed
                      time is over --budget (default 60); exit 2 when any
                      package is over, 0 when none is.

exit codes: 0 inside budget, 2 a package is over budget or the invocation
            could not run (bad flag, unreadable stdin).

example:
  nova-ci help
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
		return refuse(stderr, "", "no verb given; slowtests is the verb this tool exists for")
	}
	switch args[0] {
	case "slowtests":
		return cmdSlowtests(args[1:], stdin, stdout, stderr)
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
