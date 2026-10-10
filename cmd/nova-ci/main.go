// nova-ci runs the checks this repository's CI path makes on its own output.
// Its first verb, slowtests, reads the newline-delimited `go test -json`
// TestEvents on stdin, sums the package-level elapsed time for each package,
// and prints a CI-SLOW line for every package whose total is over the budget, a
// measurement that fails the run only under --enforce, so a slow test surfaces
// the moment it happens.
//
// Every path and every budget comes from a flag. There are no guessed paths; a
// budget of zero or less is refused rather than read as unlimited.
package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/bench"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

const usage = `nova-ci: test-time budgets over go test -json output, and this repository's own CI steps

how it works: slowtests and functional work in any Go module and keep no state:
slowtests reads go test -json events on stdin and prints a CI-SLOW line for each
package or test over its budget and one CI-LOAD line; functional names the
packages holding functional-tagged tests. local, new-rule and new-verb need a
nova-tools checkout; github receipt writes one row of a CI run to a Redis store.
first run: nothing to set up: slowtests --example reads a built-in event stream.
In your own module, slowtests reads the events of the packages you name, at a
60-second budget; the commands under example: are what runs.

usage, in any Go module (no state, no store):
  nova-ci help        print this banner and the verbs below (inspection)
  nova-ci version     which build this is: <version> <goos>/<goarch> <go version>
  nova-ci slowtests [--budget <seconds> | --package-budget <s>] [--test-budget <s>]
                    [--allowlist <file>] [--sleeps <file>] [--enforce]
                    [--load <n> --cpus <n>] [--example] [--allow-empty] [--json] [--max <n>]
                      (inspection) read newline-delimited ` + "`go test -json`" + `
                      TestEvents on stdin (or the built-in example stream with
                      --example) and print one CI-SLOW line per package whose
                      total elapsed time is over its budget (--budget, whole
                      seconds, default 60; --package-budget replaces it) and
                      per top-level test over --test-budget, then one CI-LOAD
                      line. The allowlist (row shape:
                      internal/pkg<TAB>test<TAB>seconds<TAB><measured>s@<where>,
                      where is run<id> or a bench, - in the test column for a
                      package's own row; bound: budget sits between its
                      measurement and three times it, the 3x headroom ceiling)
                      raises one package's or test's budget. One row,
                      tab-separated:
                      internal/ci/slowtests	TestA	4.5	3s@run1
                      The host's load average (the
                      larger of its 1- and 5-minute figures, over its CPUs;
                      --load and --cpus give them by hand) is printed and never
                      read by the verdict. The times are a measurement: a
                      CI-SLOW line fails the run only with --enforce (the
                      nightly reference leg). A test skipped with the marker "SLEEPS:"
                      and not on --sleeps (internal/pkg<TAB>test<TAB>where) is a
                      CI-SLEEPS line and fails the run on every leg. A package
                      go test served from its test cache reports a package
                      elapsed near zero, so a cached run never trips a package
                      budget; its tests replay the cached times, which
                      --test-budget still reads (measure with -count=1). A run
                      with more finding lines than --max prints the first --max
                      and one CI-SLOW MORE shown=<n> total=<n> line naming the
                      flag that prints the rest; --max 0 prints every finding.
                      --json prints the same verdict as one JSON object.
  nova-ci functional <package-dir>...
                      (inspection) print the packages among these that hold
                      functional tests (a _test.go built only under the
                      functional build tag) on one line and a go test -run
                      pattern naming exactly those tests on the next; when
                      there are none, one line
                      CI FUNCTIONAL OK packages=0 reason=<why>. A flag, and a
                      pattern matching no package, are refused.

usage, in a nova-tools checkout (this repository's own CI steps):
  nova-ci local [--base origin/dev] [--functional] [--dry-run]
                      (runs tests, writes only a temp dir; needs a nova-tools
                      checkout) the unit tier CI runs for this diff, on this
                      machine: the packages CI's selection picks against the
                      merge base of --base and HEAD, run through the Makefile's
                      test target (its go test flags and its slowtests budgets)
                      under nice -n 15 at -p 2, GOMAXPROCS=2 and -count=1; one
                      PKG line per package with its seconds, one RED line per
                      failing test with its output. --functional adds the
                      functional build tag (GOTEST_TAGS=functional); CI runs
                      those tests in its functional job as a stream merges.
                      --dry-run prints the packages and the make line, and
                      runs nothing.
  nova-ci new-rule [--root <checkout>] [--dry-run] <rule-name>
                      (local write; needs a nova-tools checkout) scaffold a new
                      class rule: class test, fixture and make target;
                      --dry-run lists the files and writes nothing
  nova-ci new-verb [--root <checkout>] [--dry-run] <tool> <verb>
                      (local write; needs a nova-tools checkout) scaffold a new
                      verb of an existing tool: command, test, fixture and make
                      target; --dry-run lists the files and writes nothing
  nova-ci bench run --host <h> [--fallback <h>] --dir <tree> [--root <dir>] [--cache <dir>] [--with-git] -- <go command>
                      (delivery: writes only its own run directory on the bench)
                      copy the tree, .git left out unless --with-git, to a fresh
                      run directory on the Linux bench --host (--fallback when it
                      does not answer), run the command in it under nice -n 19
                      with GOCACHE, GOFLAGS=-mod=readonly and NOVA_TEST_NO_HOST=1,
                      stream its output, then remove that directory and nothing
                      else. One CI BENCH line on stderr ends the run.
                      example: nova-ci bench run --host <bench> --dir . -- go vet ./cmd/nova-ci/
  nova-ci github receipt --from-runner --redis <addr> --repo owner/name
                    --sha <40hex> --run-id <n> --workflow <name>
                    --conclusion success|failure|cancelled [--pr <n>] [--at <rfc3339>]
                    [--dry-run]
                      (store write) the ci-ok job's run receipt: one ev:github
                      row of the workflow_run shape, sender runner; dialled as
                      the environment's seat (NOVA_SPRINT_REDIS_USER),
                      with the password in the variable NOVA_SPRINT_REDIS_PASSWORD_ENV names, never on the line.
                      A refused write is tried once, not retried. One CI
                      RECEIPT line. --dry-run checks the fields and prints the
                      line with ev=-, dialling nothing.

exit codes: 0 done, 1 the verb said no (slowtests, local, github receipt), 2 usage or could not run; by verb:
  slowtests: 0 inside budget, or CI-SLOW lines without --enforce (a
    measurement), or an empty stream with --allow-empty; 1 a CI-SLEEPS
    line, a truncated package (started and never ended), a CI-SLOW line
    under --enforce, or an empty stream without --allow-empty (the check
    said no); 2 the invocation could not run (bad flag, unreadable stdin)
  local: 0 green; 1 a red test, a package that did not build, or a
    CI-SLEEPS line; 2 a step that could not run, or usage
  functional: 0 the selection printed (packages=0 included); 2 a flag, or
    a pattern that matches no package
  new-rule: 0 the files written (or listed, with --dry-run); 2 usage, not a
    checkout, a bad name, or a file already there
  new-verb: 0 the files written (or listed, with --dry-run); 2 usage, not a
    checkout, a bad name, a tool with no func main, or a file already there
  bench run: the command's own exit status; 2 also usage, or a run
    that never reached the command (no bench answered, the copy failed),
    told apart by its REFUSED line and the missing CI BENCH exit=<n>
  github receipt: 0 written (or checked, with --dry-run); 1 the store
    refused the write or could not confirm it; 2 usage or a refused field
  version: 0 printed; 2 an argument given

example:
  nova-ci help
  nova-ci slowtests --example --budget 60 --load 4 --cpus 16
  nova-ci slowtests --example --budget 120 --load 4 --cpus 16

slowtests judges timing, not test success; with set -o pipefail the pipeline's exit carries go test -timeout 600s's.
`

// verbs is every verb in the order the banner lists them: what a refusal for a
// missing or unknown verb names.
const verbs = "slowtests, functional, local, new-rule, new-verb, bench run, github receipt, version, help"

// helpListsVerb reports whether tool's help already lists verb, so new-verb
// refuses a name that would stand for two verbs. nova-ci's list is the verbs
// constant; another tool's help is not this banner, so this reports false for
// it (docs/STANDARD.md section 3).
func helpListsVerb(tool, verb string) bool {
	if tool != "nova-ci" {
		return false
	}
	for _, listed := range strings.Split(verbs, ", ") {
		if listed == verb {
			return true
		}
	}
	return false
}

// verbEffect is what running a verb does beyond printing, the last line of its -h, in
// pkg/tool's words (inspection, local write or delivery; docs/STANDARD.md section 2).
var verbEffect = map[string]tool.Effect{
	"slowtests":      tool.Inspection,
	"functional":     tool.Inspection,
	"version":        tool.Inspection,
	"local":          "local write: runs this checkout's unit tests, writing only a temp dir",
	"new-rule":       tool.LocalWrite,
	"new-verb":       tool.LocalWrite,
	"bench run":      benchEffect,
	"github receipt": "delivery: writes one row of a CI run to a Redis store",
}

// refuse prints this tool's one refusal line, `nova-ci[ <verb>] REFUSED:
// <what>; run: <remedy>` (STANDARD §2), at exit 2. The remedy is the verb's
// own help, or the banner when no verb was named. Package flag is given no
// stream so an argument holding a newline cannot author a second line of
// stderr before this code runs.
func refuse(stderr io.Writer, where, what string) int {
	next := "nova-ci help"
	if where != "" {
		next = "nova-ci" + where + " -h"
	}
	return refuseRun(stderr, where, what, next)
}

// refuseRun is refuse with a remedy of the verb's choosing: a command that
// fixes the call, where the help would only describe it.
func refuseRun(stderr io.Writer, where, what, next string) int {
	fmt.Fprintf(stderr, "nova-ci%s REFUSED: %s; run: %s\n", where, oneline.Escape(what), oneline.Escape(next))
	return 2
}

// exitTable is the exit-code paragraph a verb's -h quotes: the banner's first
// line and that verb's own row, so no verb's help states another verb's codes.
// A verb the table does not name (help) quotes the whole paragraph.
func exitTable(verb string) string {
	head, rest, _ := strings.Cut(usage, "\nexit codes: ")
	table, tail, _ := strings.Cut(rest, "\n\n")
	lines := strings.Split(table, "\n")
	var own []string
	for i := 1; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "  "+verb+":") && !strings.HasPrefix(lines[i], "  "+verb+" ") {
			continue
		}
		own = append(own, lines[i])
		for i+1 < len(lines) && strings.HasPrefix(lines[i+1], "    ") {
			i++
			own = append(own, lines[i])
		}
	}
	if len(own) == 0 {
		return usage
	}
	return head + "\nexit codes: " + lines[0] + "\n" + strings.Join(own, "\n") + "\n\n" + tail
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	// `<verb> -h` and `help <verb>` print that verb's help on stdout at exit 0,
	// before anything is read, run or written (the CLI style's rule (b)),
	// with that verb's own exit codes (verbflag.RecoverWith would quote the
	// whole table).
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		h, ok := r.(verbflag.Help)
		if !ok {
			panic(r)
		}
		verb := verbflag.Verb("nova-ci", h.FS)
		verbflag.Print(stdout, "nova-ci", exitTable(verb), h.FS)
		if e, ok := verbEffect[verb]; ok {
			fmt.Fprintf(stdout, "effect: %s\n", e)
		}
		code = 0
	}()
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; the verbs are "+verbs)
	}
	switch args[0] {
	case "version", "--version":
		return cmdVersion(args[1:], stdout, stderr)
	case "slowtests":
		return cmdSlowtests(args[1:], stdin, stdout, stderr)
	case "local":
		return cmdLocal(args[1:], stdout, stderr, execLocal, localSelectThrough(execLocal))
	case "functional":
		return cmdFunctional(args[1:], stdout, stderr)
	case "new-rule":
		return cmdNewRule(args[1:], stdout, stderr)
	case "new-verb":
		return cmdNewVerb(args[1:], stdout, stderr)
	case "bench":
		return cmdBench(context.Background(), args[1:], stdin, stdout, stderr, bench.Exec{})
	case "github":
		return cmdGitHub(args[1:], stdout, stderr, os.Getenv)
	case "help", "-h", "--help":
		if args[0] == "help" && len(args) > 1 && args[1] != "help" && !verbflag.IsHelp(args[1]) {
			return run(append(args[1:], "--help"), stdin, stdout, stderr)
		}
		fmt.Fprint(stdout, usage)
		return 0
	default:
		return refuse(stderr, "", fmt.Sprintf("unknown verb %q; the verbs are %s", args[0], verbs))
	}
}
