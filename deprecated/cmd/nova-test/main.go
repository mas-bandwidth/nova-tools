// Command nova-test is the validation-layer tool of docs/SPEC-TEST.md. This
// slice implements one verb of the first slice, `status --since`, over a run
// store (nova-tools #2207); plan, run, failures and receipt come on their own
// cards. Every path and every window comes from a flag: there is no default
// store and no default window, and a missing flag is a refusal, never a guess.
// Exit 0 ran, 2 could not run.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

var version string

const usage = `nova-test: reproducible repository validation (see docs/SPEC-TEST.md)

usage:
  nova-test version
  nova-test status --store <dir> --since <rfc3339|duration> [--now <rfc3339>] [--max <n>]

flags:
  --store <dir>     the run store: one <run>.json file per run directly under
                    the directory. Required: there is no default store.
  --since <when>    the survey boundary. An RFC 3339 instant, or a duration
                    (15m, 24h) counted back from the clock. A run is listed when
                    it was queued at or after the boundary. Required.
  --now <rfc3339>   the clock, for tests and replays. Default is the real clock
                    in UTC.
  --max <n>         run lines to print before one MORE line stands for the rest.
                    Default 20, and 0 prints all. The summary always carries the
                    whole count.

Each run line carries queue (queued to started), drain (cancel requested to
cancelled), exec (started to finished or cancelled) and e2e (queued to finished
or cancelled); a value still open at the clock ends in +, and - means the
interval never began. attempts lists every attempt id in order, and
prior_failures names each failed attempt before the last with its failure.

exit codes: 0 ran, 2 could not run (bad invocation, unreadable store or run).

example:
  nova-test status --store cmd/nova-test/testdata/runs --since 2026-09-23T00:00:00Z --now 2026-09-23T10:30:00Z
  nova-test version
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; status surveys recent runs")
	}
	switch args[0] {
	case "status":
		return cmdStatus(args[1:], stdout, stderr)
	case "version", "--version":
		if len(args) > 1 {
			return refuse(stderr, " version", fmt.Sprintf("takes no flags and no arguments, got %d", len(args)-1))
		}
		fmt.Fprintln(stdout, buildinfo.Line("nova-test", version))
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		return refuse(stderr, "", fmt.Sprintf("unknown subcommand %q", args[0]))
	}
}

// refuse is one line naming what was wrong and the door to the usage.
func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-test%s: %s; run: nova-test help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

// parse runs a flag set and reports every missing required flag, not the first.
func parse(fs *flag.FlagSet, args []string, stderr io.Writer, required ...string) (map[string]bool, bool) {
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		refuse(stderr, " "+fs.Name(), oneline.Cap(err.Error(), oneline.TailBytes))
		return nil, false
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	sort.Strings(required)
	ok := true
	for _, name := range required {
		if !given[name] {
			refuse(stderr, " "+fs.Name(), fmt.Sprintf("--%s is required; refusing to guess", name))
			ok = false
		}
	}
	if ok && fs.NArg() > 0 {
		refuse(stderr, " "+fs.Name(), fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
		ok = false
	}
	return given, ok
}

// instant parses an RFC 3339 value into UTC.
func instant(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	return t.UTC(), err
}
