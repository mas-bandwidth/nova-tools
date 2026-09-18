package main

// failed.go is the `failed` verb: name a run, a pull request or a branch, and get the
// failing tests of its failing jobs -- the package, the test, the file and the line, and
// the test's own words -- instead of four megabytes of log. It is the pipeline of gh, tr,
// sed and grep Rowan typed six times on 2026-09-18, made a verb, so the seventh time is
// one line and the eighth is a machine's.
//
// The verb owns the flags and the printing; internal/ci owns the forge and the parser.

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const failedUsage = `nova-ci failed: the failing tests of a run's failing jobs, in one line each (see docs/SPEC-CI.md)

usage:
  nova-ci failed --repo <owner/name> (--run <id> | --pr <n> [--merge-group] | --branch <name>)
                 [--job <text>] [--max-lines <n>] [--gh <path>] [--timeout <duration>]

  --repo <owner/name>   the repository the run belongs to; there is no default
  --run <id>            the run to read, by its id
  --pr <n>              the latest run of that pull request's head commit
  --merge-group         with --pr: the latest merge_group run of its queue branch
  --branch <name>       the latest run on that branch
  --job <text>          read only the jobs whose name contains this text
  --max-lines <n>       how many of a test's own message lines to print (default 8);
                        the rest are counted, never dropped in silence
  --gh <path>           path to the gh executable (default: gh)
  --timeout <duration>  budget for one gh call (default: 2m)

output (a job name is quoted, because it is what you paste back into --job):
  FAILED job="<name>" pkg=<pkg> test=<Test> at=<file:line>
      <the test's own message lines, indented as it printed them>
  CANCELLED job="<name>" step="<name>" after=<d>
  TIMEOUT job="<name>" pkg=<pkg> running=<tests, three then a count>
  NOLOG job="<name>" reason="<a log the forge would not give; the run still reports>"
  FAILED OK jobs=<n> tests=<n> [unread=<n>]

exit codes: 0 nothing red in the run, 1 the run said something red,
            2 the invocation could not run (bad flag, no run, gh could not answer).

example:
  nova-ci failed --repo owner/name --run 35375346271
  nova-ci failed --repo owner/name --pr 1370 --merge-group
  nova-ci failed --repo owner/name --branch dev --job "test (3/4 studio)"
`

// cmdFailed parses the flags, asks the forge, and prints the report. newForge is the
// seam: the tests pass a fake, and nothing in this file reaches a network.
func cmdFailed(args []string, stdout, stderr io.Writer, newForge func(repo, ghPath string, timeout time.Duration) ci.FailForge) int {
	for _, a := range args {
		if a == "--help" || a == "-h" || a == "help" {
			fmt.Fprint(stdout, failedUsage)
			return 0
		}
	}
	fs := flag.NewFlagSet("failed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	repo := fs.String("repo", "", "the repository the run belongs to, as <owner>/<name>")
	run := fs.Int64("run", 0, "the run to read, by its id")
	pr := fs.Int("pr", 0, "the pull request whose latest run to read")
	branch := fs.String("branch", "", "the branch whose latest run to read")
	mergeGroup := fs.Bool("merge-group", false, "with --pr, the latest merge_group run of its queue branch")
	job := fs.String("job", "", "read only the jobs whose name contains this text")
	maxLines := fs.Int("max-lines", ci.DefaultFailedMaxLines, "how many of a test's own message lines to print")
	ghPath := fs.String("gh", "gh", "path to the gh executable")
	timeout := fs.Duration("timeout", 2*time.Minute, "budget for one gh call")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " failed", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " failed", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.Count(*repo, "/") != 1 || strings.HasPrefix(*repo, "/") || strings.HasSuffix(*repo, "/") {
		return refuse(stderr, " failed", fmt.Sprintf("--repo wants the repository as <owner>/<name>, and there is no default (got %q)", oneline.Field(*repo)))
	}
	named := 0
	for _, given := range []bool{*run > 0, *pr > 0, *branch != ""} {
		if given {
			named++
		}
	}
	if named != 1 {
		return refuse(stderr, " failed", "name exactly one run: --run <id>, --pr <n> or --branch <name>")
	}
	if *mergeGroup && *pr <= 0 {
		return refuse(stderr, " failed", "--merge-group is the merge queue's run of a pull request; it wants --pr <n>")
	}
	if *maxLines <= 0 {
		return refuse(stderr, " failed", fmt.Sprintf("--max-lines must be a whole number greater than zero (got %d)", *maxLines))
	}
	if *timeout <= 0 {
		return refuse(stderr, " failed", fmt.Sprintf("--timeout must be a positive duration, like 2m (got %s)", *timeout))
	}

	forge := newForge(*repo, *ghPath, *timeout)
	_, report, err := ci.ReadFailedRun(forge, ci.RunSelector{
		Run:        *run,
		PR:         *pr,
		Branch:     *branch,
		MergeGroup: *mergeGroup,
	}, *job)
	if err != nil {
		return refuse(stderr, " failed", oneline.Cap(oneline.Err(err), oneline.TailBytes))
	}
	for _, line := range report.Lines(*maxLines) {
		fmt.Fprintln(stdout, line)
	}
	return report.ExitCode()
}

// ghForge is the forge the binary runs with: the real one, over gh.
func ghForge(repo, ghPath string, timeout time.Duration) ci.FailForge {
	return ci.NewGHFailForge(repo, ghPath, timeout)
}
