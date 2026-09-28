//go:build ignore

// timing.go is the committed script behind ideas #791: it prints, for the
// last 200 pull requests of mas-bandwidth/schema and
// mas-bandwidth/nova-tools, the time from PR-open to all-green split into
// queue, setup and test per job -- one TSV row per job, its three spans in
// whole seconds, and the envelope its PR opened under, "-" for a PR that
// never went all-green. The next-sprint report of 2026-09-22 asked for the
// measurement ("Cards to cut"): how long a pull request waits before its
// first runner, how long setup holds it, how long its tests run, is the
// split every "CI feels slow" argument actually turns on.
//
// It reads a harvested events log, one JSON object per line, one line per CI
// job of one pull request -- the shape internal/ci/timing.Load reads. The
// harvest is a local file and there is no default: a guessed path is the one
// thing this repository's tools never do.
//
// The script is a //go:build ignore file on purpose, the Go shape of a
// scripts/ entry: it is run as a file, not built into the binary, because
// wiring a `timing` verb into nova-ci's dispatch is one case line in
// cmd/nova-ci/main.go, and this card's PATHS does not name that file. Run it
// from a checkout of the repo:
//
//	go run cmd/nova-ci/timing.go --log <events log>
//
// The table is deterministic: the same log renders byte-for-byte the same
// table, so re-running the script reproduces the committed table --
// internal/ci/timing/testdata/table.tsv -- and
// TestTimingTableReproduces holds the script's own output against it.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ci/timing"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-ci timing: PR-open to all-green, split into queue, setup and test per job (ideas #791)

usage:
  go run cmd/nova-ci/timing.go --log <events log> [--repos <a,b>] [--last <n>]

  --log <path>   the harvested events log, one JSON object per line, one line
                 per CI job of one pull request; there is no default
  --repos <a,b>  the repositories to measure, comma-separated
                 (default: mas-bandwidth/nova-tools,mas-bandwidth/schema)
  --last <n>     per repository, the last n pull requests the log holds
                 (default: 200)

output:
  TSV on stdout: a header line, then one row per job -- repo, pr, job, opened,
  queue_s, setup_s, test_s, pr_open_to_green_s; the last is "-" for a pull
  request that never went all-green.

exit codes: 0 the table printed, 2 the invocation could not run (bad flag,
            unreadable log, a log the table cannot use).

example:
  go run cmd/nova-ci/timing.go --log internal/ci/timing/testdata/events.jsonl
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	for _, a := range args {
		if a == "--help" || a == "-h" || a == "help" {
			fmt.Fprint(stdout, usage)
			return 0
		}
	}
	fs := flag.NewFlagSet("timing", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	logPath := fs.String("log", "", "the harvested events log to read; there is no default")
	repos := fs.String("repos", strings.Join(timing.DefaultRepos, ","), "the repositories to measure, comma-separated")
	last := fs.Int("last", timing.DefaultLast, "per repository, the last n pull requests the log holds")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *logPath == "" {
		return refuse(stderr, "--log wants the path of the harvested events log; there is no default")
	}
	want := map[string]bool{}
	for _, r := range strings.Split(*repos, ",") {
		if r = strings.TrimSpace(r); r != "" {
			want[r] = true
		}
	}
	if len(want) == 0 {
		return refuse(stderr, "--repos wants the repositories to measure as a comma-separated list of <owner>/<name>")
	}
	if *last <= 0 {
		return refuse(stderr, fmt.Sprintf("--last wants a whole number of pull requests above zero (got %d)", *last))
	}
	raw, err := os.Open(*logPath)
	if err != nil {
		return refuse(stderr, fmt.Sprintf("cannot read --log: %s", oneline.Err(err)))
	}
	defer raw.Close()
	events, err := timing.Load(raw)
	if err != nil {
		return refuse(stderr, fmt.Sprintf("%s is not a log the table can use: %s", oneline.Field(*logPath), oneline.Err(err)))
	}
	rows, err := timing.Rows(timing.Select(events, keys(want), *last))
	if err != nil {
		return refuse(stderr, oneline.Err(err))
	}
	fmt.Fprint(stdout, timing.Render(rows))
	return 0
}

// keys flattens the set of wanted repositories back to a slice for Select.
func keys(want map[string]bool) []string {
	var out []string
	for r := range want {
		out = append(out, r)
	}
	return out
}

// refuse prints this script's one-line refusal and names its door.
func refuse(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "nova-ci timing: %s; run: go run cmd/nova-ci/timing.go help\n", oneline.Escape(what))
	return 2
}
