// slowtests.go holds the slowtests verb: its flags, its run and the helpers only it uses.

package main

import (
	"bufio"
	"context"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci/slowtests"
	"github.com/mas-bandwidth/nova-tools/pkg/bounded"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
	"github.com/mas-bandwidth/nova-tools/pkg/tty"
)

// hostLoad reads this host's load average for the CI-LOAD line slowtests
// prints beside the times: the larger of the 1- and 5-minute figures, so a leg
// of up to two minutes is described by the busiest reading that covers it,
// over runtime.NumCPU (the machine's logical CPUs, not the leg's GOMAXPROCS).
// It is a measurement; no verdict reads it.
func hostLoad() slowtests.Load {
	return loadFrom(runtime.GOOS, runtime.NumCPU(), readLoadAvg)
}

// readLoadAvg is the one read of the host: darwin answers `sysctl -n
// vm.loadavg` ("{ 17.36 21.31 19.56 }"), Linux /proc/loadavg ("0.52 0.58 0.59
// 1/467 12345"); any other host has no load average.
func readLoadAvg(goos string) (string, error) {
	switch goos {
	case "darwin":
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := subproc.Context(ctx, "sysctl", "-n", "vm.loadavg").Output()
		if err != nil {
			return "", fmt.Errorf("sysctl -n vm.loadavg: %w", err)
		}
		return string(out), nil
	case "linux":
		out, err := os.ReadFile("/proc/loadavg")
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
	return "", fmt.Errorf("%s has no load average", goos)
}

// loadFrom is hostLoad with the read handed in: a read that fails, or a
// figure that does not parse, is Known=false with the reason, and the CI-LOAD
// line says so.
func loadFrom(goos string, cpus int, read func(goos string) (string, error)) slowtests.Load {
	load := slowtests.Load{CPUs: cpus}
	raw, err := read(goos)
	if err != nil {
		load.Why = err.Error()
		return load
	}
	avg, err := parseLoadAvg(raw)
	if err != nil {
		load.Why = err.Error()
		return load
	}
	load.Avg, load.Known = avg, true
	return load
}

// parseLoadAvg reads the first two figures of a sysctl vm.loadavg or
// /proc/loadavg line (braces ignored) and returns the larger.
func parseLoadAvg(raw string) (float64, error) {
	fields := strings.Fields(strings.NewReplacer("{", " ", "}", " ").Replace(raw))
	if len(fields) < 2 {
		return 0, fmt.Errorf("load average %q has fewer than two figures", strings.TrimSpace(raw))
	}
	var max float64
	for _, f := range fields[:2] {
		v, err := strconv.ParseFloat(f, 64)
		if err != nil || v < 0 {
			return 0, fmt.Errorf("load average %q: %q is not a load", strings.TrimSpace(raw), f)
		}
		if v > max {
			max = v
		}
	}
	return max, nil
}

// exampleEvents is the go test -json stream slowtests --example reads: two
// packages, one over the default budget, so a first run needs no Go module
// and no file from a source checkout.
//
//go:embed testdata/example-events.jsonl
var exampleEvents string

// cmdSlowtests reads the events, sums them against the budgets, and prints one
// CI-SLOW line per package or test over its budget (or the single CI-SLOW OK
// line), one CI-SLEEPS line per unledgered SLEEPS skip, one `truncated: <pkg>
// started and never ended` line per package with a start event and no
// package-level terminal event, and the CI-LOAD line. Exit 1 on a CI-SLEEPS
// line or a truncated package on every leg, and on a CI-SLOW line only with
// --enforce; 0 otherwise.
// A malformed line or an unusable flag is a refusal, and one run names every
// problem with the flags and the files they name.
func cmdSlowtests(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("slowtests", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	budget := fs.Int("budget", 60, "whole seconds a package's tests may take before it is over budget")
	packageBudget := fs.Float64("package-budget", 0, "seconds a package's tests may take; replaces --budget when set")
	testBudget := fs.Float64("test-budget", 0, "seconds one top-level test may take; 0 judges packages only")
	allowlist := fs.String("allowlist", "", "pkg<TAB>test<TAB>seconds<TAB><measured>s@<where> rows (bound: between measurement and 3x it) that raise one package's or one test's budget")
	sleeps := fs.String("sleeps", "", "pkg<TAB>test<TAB>where rows: the tests already skipped with the marker \"SLEEPS:\"")
	enforce := fs.Bool("enforce", false, "fail the run on a CI-SLOW line (the nightly reference leg only); without it the times are printed and only a CI-SLEEPS line fails")
	loadFlag := fs.Float64("load", -1, "the host's load average, instead of reading it")
	cpusFlag := fs.Int("cpus", 0, "the host's logical CPUs, instead of runtime.NumCPU")
	example := fs.Bool("example", false, "read the built-in six-event example stream instead of stdin: a first run with no Go module")
	allowEmpty := fs.Bool("allow-empty", false, "answer OK when the count of packages read is 0; without it an empty stream is FAILED")
	asJSON := fs.Bool("json", false, "print the verdict, or the refusal, as one JSON object {result, facts, items} on stdout instead of lines")
	maxFlag := fs.Int("max", bounded.Default, "finding lines to print before one MORE line stands for the rest; 0 prints all")
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
	if *maxFlag < 0 {
		problems = append(problems, fmt.Sprintf("--max must be a line ceiling of zero or more (got %d); 0 means print them all", *maxFlag))
	}
	budgets := slowtests.Budgets{Package: float64(*budget), Test: *testBudget}
	if *packageBudget > 0 {
		budgets.Package = *packageBudget
	}
	var err error
	if budgets.Rows, err = readAllowlist(*allowlist); err != nil {
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
	// The count of packages read (Verb.Looks is not on this tree; the verb
	// applies the same rule): an empty stream is FAILED, and --allow-empty is
	// the way out. The built-in --example stream is not this check
	// (docs/STANDARD.md section 2, exit codes tell the truth). A package that
	// started and never ended is a truncated finding, not an empty stream.
	empty := report.Packages == 0 && len(report.Truncated) == 0 && !*example && !*allowEmpty
	if empty {
		code = 1
		for i, line := range lines {
			if strings.HasPrefix(line, "CI-SLOW OK ") {
				lines[i] = "CI-SLOW FAILED packages=0 slowest=none: looked at nothing; run: nova-ci slowtests --allow-empty"
			}
		}
	}
	if *asJSON {
		o := verdictJSON(report, load, *enforce, code, *maxFlag)
		if empty {
			o.Status = tool.Failed
			o.Exit = 1
			o.Why = append(o.Why, "looked at nothing: packages=0")
			o.Remedy = "nova-ci slowtests --allow-empty"
		}
		return o.Render(stdout, true)
	}
	for _, line := range capSlowLines(lines, *maxFlag) {
		fmt.Fprintln(stdout, line)
	}
	return code
}

// maxRemedy is the second half of the MORE line slowtests prints under --max.
// A cap with no remedy is censorship; a cap with one is an index
// (pkg/bounded).
const maxRemedy = "--max <n> raises the ceiling, --max 0 prints every finding"

// capSlowLines bounds the CI-SLOW finding lines Verdict returns: at most max
// of them print, then one MORE line carrying the shown and total counts, so a
// whole-tree run never buries a reader in findings (STANDARD §2: output is
// bounded and keeps its totals). The cap is over the finding lines only: the
// CI-SLEEPS lines, the OK line and the CI-LOAD line print either way, and the
// exit code is Verdict's, from the uncapped report, so capping never flips a
// verdict. max <= 0 prints everything with no MORE line.
func capSlowLines(lines []string, max int) []string {
	if max <= 0 {
		return lines
	}
	capped := 0
	for capped < len(lines) && isSlowFinding(lines[capped]) {
		capped++
	}
	if capped <= max {
		return lines
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:max]...)
	out = append(out, bounded.MoreLine("CI-SLOW", "finding", max, capped, maxRemedy))
	return append(out, lines[capped:]...)
}

// isSlowFinding reports whether line is a CI-SLOW finding: a package or a test
// over its budget. The OK, SLEEPS and LOAD lines are not findings and are
// never capped.
func isSlowFinding(line string) bool {
	return strings.HasPrefix(line, "CI-SLOW package=") || strings.HasPrefix(line, "CI-SLOW test=")
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

// readAllowlist parses the allowlist file, reporting every bad row in one refusal
// with its 1-based line number.
func readAllowlist(path string) ([]slowtests.Row, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // ignored: a read-only file

	sc := bufio.NewScanner(f)
	var errs []string
	var rows []slowtests.Row
	seen := map[string]int{}
	lineNum := 0

	for sc.Scan() {
		lineNum++
		raw := sc.Text()
		text := strings.TrimSpace(raw)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		input := strings.Repeat("\n", lineNum-1) + raw + "\n"
		parsed, parseErr := slowtests.ParseAllowlist(strings.NewReader(input))
		if parseErr != nil {
			errs = append(errs, parseErr.Error())
			continue
		}
		if len(parsed) > 0 {
			r := parsed[0]
			key := r.Package + "\t" + r.Test
			testCol := r.Test
			if testCol == "" {
				testCol = "-"
			}
			if first, dup := seen[key]; dup {
				errs = append(errs, fmt.Sprintf("line %d: %s %s is already on line %d", lineNum, r.Package, testCol, first))
				continue
			}
			seen[key] = lineNum
			rows = append(rows, r)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s: %s", path, strings.Join(errs, "; "))
	}
	return rows, nil
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
// pkg/tty, which asks the terminal for its window size: a character
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
// the same findings the lines print, typed. Exit is the verb's own. max caps
// the slow items the same way capSlowLines caps the slow lines, so the two
// renderings cannot drift: the SLEEPS items print either way.
func verdictJSON(r slowtests.Report, load slowtests.Load, enforce bool, code int, max int) *tool.Out {
	o := &tool.Out{Verb: "slowtests", Status: tool.OK, Exit: code}
	if code != 0 {
		o.Status = tool.Failed
		if len(r.Sleepers) > 0 {
			o.Why = append(o.Why, fmt.Sprintf("%d SLEEPS skip(s) not on the ledger", len(r.Sleepers)))
		}
		if enforce && len(r.Over)+len(r.OverTests) > 0 {
			o.Why = append(o.Why, fmt.Sprintf("%d CI-SLOW finding(s) under --enforce", len(r.Over)+len(r.OverTests)))
		}
		if len(r.Truncated) > 0 {
			o.Why = append(o.Why, fmt.Sprintf("%d package(s) started and never ended", len(r.Truncated)))
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
	for _, pkg := range r.Truncated {
		o.ItemText("truncated", "truncated: "+oneline.Field(pkg)+" started and never ended")
	}
	if max > 0 {
		tally := bounded.NewTally(max)
		kept := o.Items[:0]
		for _, it := range o.Items {
			if (it.Kind == "slow-package" || it.Kind == "slow-test") && !tally.Add("finding") {
				continue
			}
			kept = append(kept, it)
		}
		o.Items = kept
		if tally.Shown("finding") < tally.Total("finding") {
			o.More = append(o.More, tool.More{Kind: "finding", Shown: tally.Shown("finding"), Total: tally.Total("finding"), Remedy: maxRemedy})
		}
	}
	return o
}
