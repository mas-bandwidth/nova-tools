package decide

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/diffcheck"
)

// The gate decision (SPEC-NOVA-DECIDE section 9; docs/SPEC-SPRINT.md section 5, the gate
// verdict): a gate that failed, read failure by failure. Each failing test is classified
// flaky (a rerun at the same commit passes), caused (the card's change broke it) or
// pre-existing (it fails without the change: red at the base, or on the machine), over the
// test's first lines, whether it failed at the base, the gate's other failures, the card's
// PATHS and a summary of its diff. A failure flaky at or above the flaky bar is rerun once;
// one pre-existing at or above the pre-existing bar is reported as such, never as the
// card's; every other is caused, as a red gate was before.

// GateName is the gate decision's name in the record.
const GateName = "gate"

// The gate's classes: the class question's options, the routes a failure takes, and the
// outcome labels a rerun attaches (a rerun that is red again attaches RedAgain when the
// base was not run, since it then tells caused from pre-existing by nothing).
const (
	Flaky       = "flaky"
	Caused      = "caused"
	PreExisting = "pre-existing"
	RedAgain    = "red-again"
	// Green is a gate whose every failure was flaky and passed its rerun.
	Green = "green"
)

// MaxGateFailures is how many failing tests of one gate are asked about; a failure past it
// is caused, unasked (a gate with that many red tests is the change's).
const MaxGateFailures = 8

// maxFailureLines and maxLineBytes bound the lines a failure carries into its state.
const (
	maxFailureLines = 10
	maxLineBytes    = 300
)

// GateSchema is the gate decision's one question: the failure's class, with a probability
// per class.
func GateSchema() Schema {
	return Schema{Name: GateName, Questions: map[string]Question{
		"class": {Type: Choice, Instructions: "Why the FAILURE failed at the card's head, read from its lines, whether it " +
			"failed AT THE BASE, the gate's OTHER FAILURES, the CARD PATHS and the DIFF SUMMARY.", Criteria: map[string]string{
			Flaky:       "it fails by chance and not by the change: a timing bound, a race, a port or file in use, the machine's load; the same test at the same commit passes when run again",
			Caused:      "the card's change broke it: the test asserts what the diff altered, or reads a file the diff changed",
			PreExisting: "it fails without the card's change: it is red at the base, or fails on this machine (an operation denied, a tool or service absent) whatever the diff",
		}},
	}}
}

// Failure is one failing test of a gate's output: its package as go test names it, the
// top-level test, and the first lines it printed. A build failure has no test.
type Failure struct {
	Pkg   string   `json:"pkg"`
	Test  string   `json:"test,omitempty"`
	Lines []string `json:"lines,omitempty"`
}

// Key is the failure's name in an op id and a base-red list: <pkg>.<Test>, or the
// package alone for a build failure.
func (f Failure) Key() string {
	if f.Test == "" {
		return f.Pkg
	}
	return f.Pkg + "." + f.Test
}

var (
	failRE    = regexp.MustCompile(`^\s*--- FAIL: (Test[^\s/]*)`)
	runRE     = regexp.MustCompile(`^=== (?:RUN|CONT|PAUSE|NAME)\s+(Test[^\s/]*)`)
	pkgFailRE = regexp.MustCompile(`^FAIL\s+(\S+)(?:\s+\[(build|setup) failed\]|\s+[0-9.]+s)?\s*$`)
	pkgOKRE   = regexp.MustCompile(`^ok\s+(\S+)\s`)
	timeoutRE = regexp.MustCompile(`^\s+(Test[^\s/]*) \([0-9hms.]+\)$`)
)

// ParseGateOutput reads go test's output (plain or -v) into its failures, in the order go
// printed them: each top-level test with a `--- FAIL:` line (a subtest's failure is its
// test's), its package from the `FAIL <pkg>` line that follows, and its first lines (the
// indented lines after it, else, under -v, what it printed after its `=== RUN`); a package
// that failed to build or with no failing test named (a timeout names its test under
// "running tests:") is one failure with the package's lines. Output that is no go test's
// has no failures.
func ParseGateOutput(out string) []Failure {
	var done, pending []Failure
	var loose []string // the package's lines that belong to no failure
	byRun := map[string][]string{}
	cur, running, inTimeout := -1, "", false // cur: the pending failure indented lines belong to
	reset := func() {
		pending, loose, byRun, cur, running, inTimeout = nil, nil, map[string][]string{}, -1, "", false
	}
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(l)
		if m := failRE.FindStringSubmatch(l); m != nil {
			inTimeout = false
			if cur = slices.IndexFunc(pending, func(f Failure) bool { return f.Test == m[1] }); cur < 0 {
				pending, cur = append(pending, Failure{Test: m[1], Lines: slices.Clone(byRun[m[1]])}), len(pending)
			}
			if strings.Contains(l, m[1]+"/") && len(pending[cur].Lines) < maxFailureLines {
				pending[cur].Lines = append(pending[cur].Lines, cap1(t)) // the subtest that failed
			}
			continue
		}
		switch m := runRE.FindStringSubmatch(l); {
		case m != nil:
			cur, running = -1, m[1]
			continue
		case pkgOKRE.MatchString(l):
			reset()
			continue
		}
		if m := pkgFailRE.FindStringSubmatch(l); m != nil {
			if len(pending) == 0 {
				pending = append(pending, Failure{Lines: capLines(loose)})
			}
			for _, f := range pending {
				f.Pkg = m[1]
				done = append(done, f)
			}
			reset()
			continue
		}
		switch {
		case t == "" || t == "FAIL" || t == "PASS" || strings.HasPrefix(t, "--- PASS") || strings.HasPrefix(t, "--- SKIP"):
			continue
		case t == "running tests:":
			inTimeout = true
			continue
		case inTimeout && timeoutRE.MatchString(l):
			name := timeoutRE.FindStringSubmatch(l)[1]
			if !slices.ContainsFunc(pending, func(f Failure) bool { return f.Test == name }) {
				pending = append(pending, Failure{Test: name, Lines: capLines(loose)})
			}
			continue
		case cur >= 0 && (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")):
			if len(pending[cur].Lines) < maxFailureLines {
				pending[cur].Lines = append(pending[cur].Lines, cap1(t))
			}
			continue
		}
		cur = -1
		loose = append(loose, t)
		if running != "" && len(byRun[running]) < maxFailureLines {
			byRun[running] = append(byRun[running], cap1(t))
		}
	}
	return done
}

func capLines(ls []string) []string {
	if len(ls) > maxFailureLines {
		ls = ls[len(ls)-maxFailureLines:]
	}
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = cap1(l)
	}
	return out
}

func cap1(l string) string {
	if len(l) > maxLineBytes {
		return l[:maxLineBytes] + "..."
	}
	return l
}

// DiffSummary is the files a unified diff changes, one per line with the lines it adds
// and removes: `<path> +<added> -<removed>`, a rename as `<old> -> <new>`.
func DiffSummary(diff string) string {
	var b strings.Builder
	for _, f := range diffcheck.Parse(diff) {
		add, del := 0, 0
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				switch {
				case strings.HasPrefix(l, "+"):
					add++
				case strings.HasPrefix(l, "-"):
					del++
				}
			}
		}
		name := f.New
		if f.Old != f.New && f.Old != "" {
			name = f.Old + " -> " + f.New
		}
		fmt.Fprintf(&b, "%s +%d -%d\n", name, add, del)
	}
	return b.String()
}

// CardPaths is a card's PATHS globs: its first `PATHS:` line (cardhdr.KeyValue), split
// on commas; nil when it names none.
func CardPaths(card string) []string {
	for _, l := range strings.Split(card, "\n") {
		if k, v, ok := cardhdr.KeyValue(strings.TrimSpace(l)); ok && k == cardhdr.KeyPaths {
			var out []string
			for _, g := range strings.Split(v, ",") {
				if g = strings.TrimSpace(g); g != "" && g != "none" {
					out = append(out, g)
				}
			}
			return out
		}
	}
	return nil
}

// GateInput is what one gate's decisions are asked over: its failures, the keys of those
// red at the base (nil when the base was not run), the card's PATHS, and its diff summary.
type GateInput struct {
	Failures []Failure
	BaseRed  map[string]bool
	Paths    []string
	Diff     string // DiffSummary of the card's diff
}

// GateState is the text one failure is asked over: the failure and its lines, the base,
// the gate's other failures by name, the card's PATHS and its diff summary, each under its
// own heading.
func GateState(in GateInput, i int) string {
	f := in.Failures[i]
	var b strings.Builder
	b.WriteString("FAILURE (the test that failed at the card's head, and the first lines it printed):\n")
	if f.Test == "" {
		fmt.Fprintf(&b, "the package %s did not build or run\n", f.Pkg)
	} else {
		fmt.Fprintf(&b, "%s in %s\n", f.Test, f.Pkg)
	}
	for _, l := range f.Lines {
		b.WriteString(l + "\n")
	}
	b.WriteString("\nAT THE BASE (the same test on the commit the card started from): ")
	switch {
	case in.BaseRed == nil:
		b.WriteString("not run\n")
	case in.BaseRed[f.Key()]:
		b.WriteString("red\n")
	default:
		b.WriteString("green\n")
	}
	var others []string
	for j, o := range in.Failures {
		if j != i {
			others = append(others, o.Key())
		}
	}
	b.WriteString("\nOTHER FAILURES (the gate's other failing tests): ")
	if len(others) == 0 {
		b.WriteString("none\n")
	} else {
		b.WriteString(strings.Join(others, ", ") + "\n")
	}
	b.WriteString("\nCARD PATHS (the files the card may change): ")
	if len(in.Paths) == 0 {
		b.WriteString("none named\n")
	} else {
		b.WriteString(strings.Join(in.Paths, ", ") + "\n")
	}
	b.WriteString("\nDIFF SUMMARY (the files the card changed, lines added and removed):\n")
	if d := strings.TrimSpace(in.Diff); d == "" {
		b.WriteString("no change\n")
	} else {
		b.WriteString(d + "\n")
	}
	return b.String()
}

// GateBars are the two bars on a failure's class probabilities: at or above Flaky it is
// rerun once, at or above PreExisting it is reported pre-existing. A bar that is unset is
// Unset, above every probability, so its route is never taken: the decision is recorded
// and shown, and nothing is rerun or reclassified on it.
type GateBars struct {
	Flaky       float64 `json:"flaky"`
	PreExisting float64 `json:"pre_existing"`
}

// Unset is a gate bar the sprint row leaves empty: no probability reaches it.
const Unset = 2.0

// Set says the bar routes: it is a probability, not Unset.
func Set(bar float64) bool { return bar <= 1 }

// ParseGateBars reads the bars as the sprint row holds them, decimals as text: each a
// probability, or empty for Unset (its route is never taken; the sprint row's default);
// two set bars sum above 1, so no failure meets both. Every problem is named.
func ParseGateBars(flaky, preExisting string) (GateBars, error) {
	b := GateBars{Flaky: Unset, PreExisting: Unset}
	var p []string
	for _, f := range []struct {
		name, raw string
		to        *float64
	}{{"decide_gate_flaky", flaky, &b.Flaky}, {"decide_gate_preexisting", preExisting, &b.PreExisting}} {
		if strings.TrimSpace(f.raw) == "" {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(f.raw), 64)
		switch {
		case err != nil:
			p = append(p, fmt.Sprintf("%s %q is not a decimal", f.name, f.raw))
		case v < 0 || v > 1:
			p = append(p, fmt.Sprintf("%s %v is not a probability in [0, 1]", f.name, v))
		default:
			*f.to = v
		}
	}
	if len(p) == 0 && Set(b.Flaky) && Set(b.PreExisting) && b.Flaky+b.PreExisting <= 1 {
		p = append(p, fmt.Sprintf("decide_gate_flaky %v and decide_gate_preexisting %v sum to at most 1, so one failure could meet both; raise either", b.Flaky, b.PreExisting))
	}
	if len(p) > 0 {
		return GateBars{}, errors.New(strings.Join(p, "; "))
	}
	return b, nil
}

// Route is where a failure with this decision goes: Flaky (rerun once), PreExisting, or
// Caused.
func (b GateBars) Route(d Decision) string {
	class := d.Answers["class"]
	switch {
	case class.Prob(Flaky) >= b.Flaky:
		return Flaky
	case class.Prob(PreExisting) >= b.PreExisting:
		return PreExisting
	}
	return Caused
}

// GateCall is one failure of a gate and what was decided about it: its decision (nil for
// one not asked: a build failure, or one past MaxGateFailures) and its route.
type GateCall struct {
	Failure  Failure
	Decision *Decision
	Route    string
	Existing bool // the decision was in the record already, and nothing was asked
}

// GateResult is a gate's decisions and the route the gate takes: Caused when any failure
// is, else Flaky when any is (those are rerun), else PreExisting; "" when the gate had no
// failure, so nothing was decided (never PreExisting).
type GateResult struct {
	Calls []GateCall
	Route string
}

// Rerun is the failures the gate reruns: those routed Flaky.
func (r GateResult) Rerun() []Failure { return r.of(Flaky) }

// PreExistingTests is the failures routed PreExisting.
func (r GateResult) PreExistingTests() []Failure { return r.of(PreExisting) }

func (r GateResult) of(route string) []Failure {
	var out []Failure
	for _, c := range r.Calls {
		if c.Route == route {
			out = append(out, c.Failure)
		}
	}
	return out
}

// GateOp is a failure's op id under the gate's op: <op>/<pkg>.<Test>.
func GateOp(op string, f Failure) string { return op + "/" + f.Key() }

// Gate asks the gate decision of each failure of in (up to MaxGateFailures; a build
// failure is caused, unasked), records each under GateOp(op, failure), and routes the gate
// at the bars. A backend that fails is an error and the gate is the card's, as before. No
// failure is no decision and no route: nothing is asked or recorded.
func Gate(ctx context.Context, b Backend, bars GateBars, in GateInput, record, op string, at time.Time) (GateResult, error) {
	var r GateResult
	if len(in.Failures) == 0 {
		return r, nil
	}
	for i, f := range in.Failures {
		call := GateCall{Failure: f, Route: Caused}
		if f.Test != "" && i < MaxGateFailures {
			state := GateState(in, i)
			inputs := map[string]string{"failure": f.Key(), "state_sha256": Sum([]byte(state))}
			d, existing, err := Make(ctx, b, GateSchema(), state, record, GateOp(op, f), inputs, at)
			if err != nil {
				return GateResult{}, err
			}
			call.Decision, call.Route, call.Existing = &d, bars.Route(d), existing
		}
		r.Calls = append(r.Calls, call)
	}
	r.Route = PreExisting
	for _, c := range r.Calls {
		switch {
		case c.Route == Caused:
			r.Route = Caused
		case c.Route == Flaky && r.Route != Caused:
			r.Route = Flaky
		}
	}
	return r, nil
}

// SettleGate attaches each rerun failure's result as its decision's outcome: Flaky when the
// rerun passed; red again, PreExisting when it was red at the base, Caused when the base
// was run and green, RedAgain when the base was not run. It returns the gate's route after
// the rerun: Caused when a rerun failure is red again, else PreExisting when a failure
// was routed so, else Green.
func SettleGate(record string, r GateResult, red map[string]bool, baseRed map[string]bool, at time.Time) (string, error) {
	route := Green
	if len(r.PreExistingTests()) > 0 {
		route = PreExisting
	}
	var errs []error
	for _, c := range r.Calls {
		if c.Route != Flaky || c.Decision == nil {
			continue
		}
		label, note := Flaky, "the rerun at the same head passed"
		if red[c.Failure.Key()] {
			route = Caused
			switch label, note = RedAgain, "the rerun at the same head failed again; the base was not run"; {
			case baseRed != nil && baseRed[c.Failure.Key()]:
				label, note = PreExisting, "the rerun failed again, and the test is red at the base"
			case baseRed != nil:
				label, note = Caused, "the rerun failed again, and the test is green at the base"
			}
		}
		if _, _, err := Attach(record, Outcome{ID: c.Decision.ID, Label: label, Note: note, At: at.UTC().Format(time.RFC3339)}); err != nil {
			errs = append(errs, err)
		}
	}
	return route, errors.Join(errs...)
}

// Classes is what each asked failure was classed, for a line that shows the decisions:
// `<Test>:<class>:<p>` comma-separated, p the class's probability to two places; an unasked
// failure is `<Test>:unasked`.
func (r GateResult) Classes() string {
	out := make([]string, len(r.Calls))
	for i, c := range r.Calls {
		name := c.Failure.Test
		if name == "" {
			name = c.Failure.Pkg
		}
		if c.Decision == nil {
			out[i] = name + ":unasked"
			continue
		}
		a := c.Decision.Answers["class"]
		out[i] = fmt.Sprintf("%s:%s:%.2f", name, a.Value, a.Prob(a.Value))
	}
	return strings.Join(out, ",")
}

// Names is the failures' tests, comma-separated, a build failure as its package.
func Names(fs []Failure) string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Test
		if f.Test == "" {
			out[i] = f.Pkg
		}
	}
	return strings.Join(out, ", ")
}

// Keys is the failures' keys.
func Keys(fs []Failure) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Key()
	}
	return out
}
