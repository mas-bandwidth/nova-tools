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
	_ "embed"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ci/functional"
	"github.com/mas-bandwidth/nova-tools/internal/ci/slowtests"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/mas-bandwidth/nova-tools/internal/tty"
)

// exampleEvents is the go test -json stream slowtests --example reads: two
// packages, one over the default budget, so a first run needs no Go module
// and no file from a source checkout.
//
//go:embed testdata/example-events.jsonl
var exampleEvents string

const usage = `nova-ci: test-time budgets over go test -json output, and this repository's own CI steps

how it works: slowtests and functional work in any Go module and keep no state:
slowtests reads go test -json events on stdin and prints a CI-SLOW line for each
package or test over its budget and one CI-LOAD line; functional names the
packages holding functional-tagged tests. local, new-rule and new-verb need a
nova-tools checkout; github receipt writes one row of a CI run to a Redis store.
first run: nothing to set up: slowtests --example reads a built-in event stream.
In your own module: go test -json <packages> | nova-ci slowtests --budget 60.

usage, in any Go module (no state, no store):
  nova-ci help        print this banner and the verbs below (inspection)
  nova-ci version     which build this is: <version> <goos>/<goarch> <go version>
  nova-ci slowtests [--budget <seconds> | --package-budget <s>] [--test-budget <s>]
                    [--allowlist <file>] [--sleeps <file>] [--enforce]
                    [--load <n> --cpus <n>] [--example] [--json]
                      (inspection) read newline-delimited ` + "`go test -json`" + `
                      TestEvents on stdin (or the built-in example stream with
                      --example) and print one CI-SLOW line per package whose
                      total elapsed time is over its budget (--budget, whole
                      seconds, default 60; --package-budget replaces it) and
                      per top-level test over --test-budget, then one CI-LOAD
                      line. The allowlist (pkg<TAB>test<TAB>seconds<TAB>
                      <measured>s@<where>, where is run<id> or a bench, - in
                      the test column for a package's own row) raises one
                      package's or test's budget. The host's load average (the
                      larger of its 1- and 5-minute figures, over its CPUs;
                      --load and --cpus give them by hand) is printed and never
                      read by the verdict. The times are a measurement: a
                      CI-SLOW line fails the run only with --enforce (the
                      nightly reference leg). A test skipped with the SLEEPS
                      marker and not on --sleeps (pkg<TAB>test<TAB>where) is a
                      CI-SLEEPS line and fails the run on every leg. A package
                      go test served from its test cache reports a package
                      elapsed near zero, so a cached run never trips a package
                      budget; its tests replay the cached times, which
                      --test-budget still reads (measure with -count=1).
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
  nova-ci github receipt --from-runner --redis <addr> --repo owner/name
                    --sha <40hex> --run-id <n> --workflow <name>
                    --conclusion success|failure|cancelled [--pr <n>] [--at <rfc3339>]
                    [--dry-run]
                      (store write) the ci-ok job's run receipt: one ev:github
                      row of the workflow_run shape, sender runner; dialled as
                      the environment's seat (NOVA_SPRINT_REDIS_USER). One CI
                      RECEIPT line. --dry-run checks the fields and prints the
                      line with ev=-, dialling nothing.

exit codes: 0 done and 2 usage or could not run, for every verb; by verb:
  slowtests: 0 inside budget, or CI-SLOW lines without --enforce (a
    measurement); 1 a CI-SLEEPS line, or a CI-SLOW line under --enforce
    (the check said no); 2 the invocation could not run (bad flag,
    unreadable stdin)
  local: 0 green; 1 a red test, a package that did not build, or a
    CI-SLEEPS line; 2 a step that could not run, or usage
  functional: 0 the selection printed (packages=0 included); 2 a flag, or
    a pattern that matches no package
  new-rule: 0 the files written (or listed, with --dry-run); 2 usage, not a
    checkout, a bad name, or a file already there
  new-verb: 0 the files written (or listed, with --dry-run); 2 usage, not a
    checkout, a bad name, a tool with no func main, or a file already there
  github receipt: 0 written (or checked, with --dry-run); 1 the store
    refused the write or could not confirm it; 2 usage or a refused field
  version: 0 printed; 2 an argument given

example:
  nova-ci help
  nova-ci slowtests --example --budget 60 --load 4 --cpus 16
  nova-ci slowtests --example --budget 120 --load 4 --cpus 16
`

// verbs is every verb in the order the banner lists them: what a refusal for a
// missing or unknown verb names.
const verbs = "slowtests, functional, local, new-rule, new-verb, github receipt, version, help"

// verbEffect is what running a verb does beyond printing, the last line of its -h, in
// internal/tool's words (inspection, local write or delivery; docs/STANDARD.md section 2).
var verbEffect = map[string]tool.Effect{
	"slowtests":      tool.Inspection,
	"functional":     tool.Inspection,
	"version":        tool.Inspection,
	"local":          "local write: runs this checkout's unit tests, writing only a temp dir",
	"new-rule":       tool.LocalWrite,
	"new-verb":       tool.LocalWrite,
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

// cmdSlowtests reads the events, sums them against the budgets, and prints one
// CI-SLOW line per package or test over its budget (or the single CI-SLOW OK
// line), one CI-SLEEPS line per unledgered SLEEPS skip, and the CI-LOAD line.
// Exit 1 on a CI-SLEEPS line on every leg, and on a CI-SLOW line only with
// --enforce; 0 otherwise. A malformed line or an unusable flag is a refusal,
// and one run names every problem with the flags and the files they name.
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
	example := fs.Bool("example", false, "read the built-in six-event example stream instead of stdin: a first run with no Go module")
	asJSON := fs.Bool("json", false, "print the verdict, or the refusal, as one JSON object {result, facts, items} on stdout instead of lines")
	if err := verbflag.Parse(fs, args); err != nil {
		return slowtestsRefuse(stdout, stderr, verbflag.BoolAsked(args, "json"), []string{verbflag.Explain(fs, err)}, "nova-ci slowtests -h")
	}
	problems := nonFinite(fs)
	if fs.NArg() > 0 {
		problems = append(problems, fmt.Sprintf("unexpected argument %q (the events come on stdin, the budgets from flags)", fs.Arg(0)))
	}
	if *budget <= 0 {
		problems = append(problems, fmt.Sprintf("--budget must be a whole number of seconds greater than zero (got %d)", *budget))
	}
	// -Inf is named once, by nonFinite, not again as a negative.
	if negative := func(v float64) bool { return v < 0 && !math.IsInf(v, -1) }; negative(*packageBudget) || negative(*testBudget) {
		problems = append(problems, "--package-budget and --test-budget want seconds, zero or more (0 leaves the package budget to --budget, and judges no test alone)")
	}
	if *cpusFlag < 0 {
		problems = append(problems, "--cpus must not be negative")
	}
	budgets := slowtests.Budgets{Package: float64(*budget), Test: *testBudget}
	if *packageBudget > 0 {
		budgets.Package = *packageBudget
	}
	var err error
	if budgets.Rows, err = readRows(*allowlist, slowtests.ParseAllowlist); err != nil {
		problems = append(problems, "--allowlist "+oneline.Err(err))
	}
	if budgets.Sleeps, err = readRows(*sleeps, slowtests.ParseSleeps); err != nil {
		problems = append(problems, "--sleeps "+oneline.Err(err))
	}
	in := stdin
	if *example {
		in = strings.NewReader(exampleEvents)
	} else if isTerminal(stdin) {
		problems = append(problems, "stdin is a terminal, and slowtests reads go test -json events on stdin: pipe them in, or pass --example for the built-in stream")
	}
	if len(problems) > 0 {
		return slowtestsRefuse(stdout, stderr, *asJSON, problems, "nova-ci slowtests -h")
	}

	events, err := slowtests.Parse(in)
	if err != nil {
		return slowtestsRefuse(stdout, stderr, *asJSON, []string{fmt.Sprintf("stdin is not newline-delimited go test -json: %s", oneline.Err(err))}, "go test -json <packages> | nova-ci slowtests --budget 60")
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
	if *asJSON {
		return verdictJSON(report, load, *enforce, code).Render(stdout, true)
	}
	for _, line := range lines {
		fmt.Fprintln(stdout, line)
	}
	return code
}

// slowtestsRefuse is slowtests' refusal: the house line on stderr, or with
// --json the same refusal as the JSON of the one output value on stdout.
func slowtestsRefuse(stdout, stderr io.Writer, asJSON bool, why []string, next string) int {
	if !asJSON {
		return refuseRun(stderr, " slowtests", strings.Join(why, "; "), next)
	}
	return (&tool.Out{Verb: "slowtests", Status: tool.Refused, Exit: 2, Why: why, Remedy: next}).Render(stdout, true)
}

// nonFinite names every float flag of fs whose value is NaN or an infinity:
// strconv parses them, and no budget, load or count is one.
func nonFinite(fs *flag.FlagSet) []string {
	var bad []string
	fs.VisitAll(func(f *flag.Flag) {
		if v, ok := f.Value.(flag.Getter).Get().(float64); ok && (math.IsNaN(v) || math.IsInf(v, 0)) {
			bad = append(bad, fmt.Sprintf("--%s wants a finite number, got %s", f.Name, f.Value.String()))
		}
	})
	return bad
}

// readRows parses the file a ledger flag names; an empty name is no rows.
func readRows[R any](path string, parse func(io.Reader) ([]R, error)) ([]R, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	rows, err := parse(f)
	// ignored: a close after the parse read the whole file; the parse error is the one returned
	_ = f.Close()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rows, nil
}

// isTerminal reports whether r is a terminal: slowtests with nothing piped in
// would wait for input that never comes, so it refuses instead. A file asks
// internal/tty, which asks the terminal for its window size: a character
// device alone is not one (/dev/null is the empty stream). A reader that
// answers IsTerminal itself is the seam a test hands in.
func isTerminal(r io.Reader) bool {
	if t, ok := r.(interface{ IsTerminal() bool }); ok {
		return t.IsTerminal()
	}
	f, ok := r.(*os.File)
	return ok && tty.IsTerminal(f)
}

// verdictJSON is the slowtests verdict as the one output value (STANDARD §2):
// the same findings the lines print, typed. Exit is the verb's own.
func verdictJSON(r slowtests.Report, load slowtests.Load, enforce bool, code int) *tool.Out {
	o := &tool.Out{Verb: "slowtests", Status: tool.OK, Exit: code}
	if code != 0 {
		o.Status = tool.Failed
		if len(r.Sleepers) > 0 {
			o.Why = append(o.Why, fmt.Sprintf("%d SLEEPS skip(s) not on the ledger", len(r.Sleepers)))
		}
		if enforce && len(r.Over)+len(r.OverTests) > 0 {
			o.Why = append(o.Why, fmt.Sprintf("%d CI-SLOW finding(s) under --enforce", len(r.Over)+len(r.OverTests)))
		}
	}
	slowest := "none"
	if r.Slowest.Name != "" {
		slowest = r.Slowest.Name
	}
	o.Fact("packages", r.Packages).Fact("slowest", slowest).Fact("slowest_seconds", r.Slowest.Seconds).Fact("enforce", enforce)
	if load.Known {
		o.Fact("load", load.Avg)
	} else {
		o.Fact("load", "unknown").Fact("load_why", load.Why)
	}
	o.Fact("cpus", load.CPUs)
	for _, p := range r.Over {
		var tests []string
		for _, t := range p.Slowest {
			tests = append(tests, t.Name+":"+slowtests.Seconds(t.Seconds))
		}
		o.Item("slow-package", "package", p.Name, "seconds", p.Seconds, "budget", p.Budget, "slowest", strings.Join(tests, ","))
	}
	for _, t := range r.OverTests {
		o.Item("slow-test", "test", t.Name, "package", t.Package, "seconds", t.Seconds, "budget", t.Budget)
	}
	for _, s := range r.Sleepers {
		o.Item("sleeps", "test", s.Name, "package", s.Package)
	}
	return o
}

// cmdFunctional prints the functional tier's selection for `make
// test-functional`: the package directories among args that hold functional
// tests, all on one line, then one -run pattern naming exactly those tests.
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
