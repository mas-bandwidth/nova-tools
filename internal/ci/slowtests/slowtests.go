// Package slowtests is the machine behind cmd/nova-ci's `slowtests` verb in
// docs/SPEC-CI.md, "The per-package test time budget". It reads the
// newline-delimited TestEvent objects `go test -json` writes, sums the
// package-level Elapsed for each package, keeps the slowest few test-level rows
// so a finding can name where the time went, and reports every package whose
// total is over the budget. It reads and writes no file and never touches the
// network or the clock: the events and the budget come from the caller, so a
// test feeds canned lines and asserts a verdict.
package slowtests

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Event is one decoded TestEvent line. The field names are `go test -json`'s
// own; a package-level event has Test == "" and carries the package's total
// Elapsed, while a test-level event carries that one test's Elapsed. Output is
// the text of an output (or build-output) event: the budget check never reads
// it, and `nova-ci local` prints it under a red test so the failure is named
// with its own words.
type Event struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

// Test is one test-level row kept for a package's slowest list.
type Test struct {
	Name    string
	Seconds float64
}

// Package is one package's summed elapsed time and its slowest tests, sorted
// worst first and capped at testsKept. Budget is the seconds it was judged
// against: the default, or its allowlist row.
type Package struct {
	Name    string
	Seconds float64
	Slowest []Test
	Budget  float64
}

// OverTest is one top-level test over its budget: the default per-test budget,
// or its allowlist row.
type OverTest struct {
	Package string
	Name    string
	Seconds float64
	Budget  float64
}

// Report is one run of the budget check: how many packages were seen, the ones
// over budget (worst first), the tests over budget (worst first), the single
// slowest package overall, and the package budget they were judged against
// (kept only so a finding can print it).
type Report struct {
	Packages  int
	Over      []Package
	OverTests []OverTest
	Slowest   Package
	Budget    time.Duration
}

// Budgets is what Judge holds a run to. Package is the default seconds for a
// package's total; Test is the default seconds for one top-level test, zero
// meaning tests are not judged one by one. Rows are the allowlist: a row whose
// Test is empty is a package's own budget, any other row one test's.
type Budgets struct {
	Package float64
	Test    float64
	Rows    []Row
}

// Row is one allowlist line: `pkg<TAB>test<TAB>seconds`, with `-` in the test
// column for a package's own row. Package is module-relative (internal/ci) and
// matches an event's import path by its trailing path elements.
type Row struct {
	Package string
	Test    string
	Seconds float64
}

// testsKept is how many test-level rows a finding names. "A few" is three:
// enough to see whether one test or the whole package is the cost, few enough
// to stay one line.
const testsKept = 3

// terminalAction reports whether an action closes a TestEvent with an Elapsed;
// run, output, pause, cont and start are bookkeeping with no time of their own.
func terminalAction(action string) bool {
	switch action {
	case "pass", "fail", "skip":
		return true
	}
	return false
}

// Parse decodes newline-delimited TestEvent JSON. Blank lines are skipped; a
// line that is not an object is an error naming its 1-based line, never a
// silent skip, so a truncated pipe cannot read as a clean run.
func Parse(r io.Reader) ([]Event, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var events []Event
	line := 0
	for sc.Scan() {
		line++
		b := bytes.TrimSpace(sc.Bytes())
		if len(b) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(b, &ev); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		events = append(events, ev)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

// ParseAllowlist reads `pkg<TAB>test<TAB>seconds` rows. Blank lines and lines
// starting with # are skipped; `-` in the test column is the package's own row.
// A malformed row, a budget that is not a positive number, or a row written
// twice is an error naming its 1-based line.
func ParseAllowlist(r io.Reader) ([]Row, error) {
	sc := bufio.NewScanner(r)
	var rows []Row
	seen := map[string]int{}
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		f := strings.Split(text, "\t")
		if len(f) != 3 || f[0] == "" || f[1] == "" {
			return nil, fmt.Errorf("line %d: want pkg<TAB>test<TAB>seconds, got %q", line, text)
		}
		secs, err := strconv.ParseFloat(f[2], 64)
		if err != nil || secs <= 0 {
			return nil, fmt.Errorf("line %d: budget %q is not a positive number of seconds", line, f[2])
		}
		row := Row{Package: f[0], Test: f[1], Seconds: secs}
		if row.Test == "-" {
			row.Test = ""
		}
		key := row.Package + "\t" + row.Test
		if first, dup := seen[key]; dup {
			return nil, fmt.Errorf("line %d: %s %s is already on line %d", line, f[0], f[1], first)
		}
		seen[key] = line
		rows = append(rows, row)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

// matches reports whether a module-relative allowlist package names an event's
// import path: equal, or its trailing path elements.
func matches(rowPkg, importPath string) bool {
	return importPath == rowPkg || strings.HasSuffix(importPath, "/"+rowPkg)
}

// budgetFor is the row's seconds for (pkg, test), or def when no row names it.
func (b Budgets) budgetFor(pkg, test string, def float64) float64 {
	for _, row := range b.Rows {
		if row.Test == test && matches(row.Package, pkg) {
			return row.Seconds
		}
	}
	return def
}

// Sum folds the events into a report against one package budget and no
// per-test budget: Judge with Budgets{Package: budget}.
func Sum(events []Event, budget time.Duration) Report {
	return Judge(events, Budgets{Package: budget.Seconds()})
}

// Judge folds the events into a report. A package's total is the sum of its
// package-level Elapsed (Test == ""), judged against its allowlist row or
// b.Package. With b.Test > 0 every top-level test (no "/" in its name: a
// subtest's time is already its parent's) is judged against its row or b.Test.
// Over-budget packages and tests are returned worst first, ties by name, so the
// output never depends on map iteration.
func Judge(events []Event, b Budgets) Report {
	byPackage := map[string]*Package{}
	var order []string
	var overTests []OverTest
	for _, ev := range events {
		if ev.Package == "" || !terminalAction(ev.Action) {
			continue
		}
		pkg, ok := byPackage[ev.Package]
		if !ok {
			pkg = &Package{Name: ev.Package}
			byPackage[ev.Package] = pkg
			order = append(order, ev.Package)
		}
		if ev.Test == "" {
			pkg.Seconds += ev.Elapsed
			continue
		}
		if ev.Elapsed > 0 {
			pkg.Slowest = append(pkg.Slowest, Test{Name: ev.Test, Seconds: ev.Elapsed})
		}
		if b.Test > 0 && !strings.Contains(ev.Test, "/") {
			if limit := b.budgetFor(ev.Package, ev.Test, b.Test); ev.Elapsed > limit {
				overTests = append(overTests, OverTest{Package: ev.Package, Name: ev.Test, Seconds: ev.Elapsed, Budget: limit})
			}
		}
	}

	report := Report{Packages: len(order), Budget: time.Duration(b.Package * float64(time.Second)), OverTests: overTests}
	sort.SliceStable(report.OverTests, func(i, j int) bool {
		if report.OverTests[i].Seconds != report.OverTests[j].Seconds {
			return report.OverTests[i].Seconds > report.OverTests[j].Seconds
		}
		if report.OverTests[i].Package != report.OverTests[j].Package {
			return report.OverTests[i].Package < report.OverTests[j].Package
		}
		return report.OverTests[i].Name < report.OverTests[j].Name
	})
	for _, name := range order {
		pkg := byPackage[name]
		sort.SliceStable(pkg.Slowest, func(i, j int) bool {
			return pkg.Slowest[i].Seconds > pkg.Slowest[j].Seconds
		})
		if len(pkg.Slowest) > testsKept {
			pkg.Slowest = pkg.Slowest[:testsKept]
		}
		if report.Slowest.Name == "" || pkg.Seconds > report.Slowest.Seconds {
			report.Slowest = *pkg
		}
		pkg.Budget = b.budgetFor(name, "", b.Package)
		if pkg.Seconds > pkg.Budget {
			report.Over = append(report.Over, *pkg)
		}
	}
	sort.SliceStable(report.Over, func(i, j int) bool {
		if report.Over[i].Seconds != report.Over[j].Seconds {
			return report.Over[i].Seconds > report.Over[j].Seconds
		}
		return report.Over[i].Name < report.Over[j].Name
	})
	return report
}

// Seconds renders a duration in seconds to one decimal, with the unit: 3.2
// becomes "3.2s" and 70.0 stays "70.0s", so two adjacent rows compare without a
// reader doing arithmetic.
func Seconds(seconds float64) string {
	return strconv.FormatFloat(seconds, 'f', 1, 64) + "s"
}

// budgetText renders a budget in seconds without a trailing .0: 60 stays
// "60s", 1.5 is "1.5s".
func budgetText(seconds float64) string {
	return strconv.FormatFloat(seconds, 'f', -1, 64) + "s"
}

// ExitCode is 2 when any package or test is over budget, 0 when none is.
func (r Report) ExitCode() int {
	if len(r.Over) > 0 || len(r.OverTests) > 0 {
		return 2
	}
	return 0
}

// OverLines is one line per over-budget package, worst first, then one per
// over-budget test, worst first.
func (r Report) OverLines() []string {
	lines := make([]string, 0, len(r.Over)+len(r.OverTests))
	for _, pkg := range r.Over {
		lines = append(lines, fmt.Sprintf("CI-SLOW package=%s seconds=%s budget=%s slowest=%s",
			oneline.Field(pkg.Name), Seconds(pkg.Seconds), budgetText(pkg.Budget), slowestList(pkg)))
	}
	for _, test := range r.OverTests {
		lines = append(lines, fmt.Sprintf("CI-SLOW test=%s package=%s seconds=%s budget=%s",
			oneline.Field(test.Name), oneline.Field(test.Package), Seconds(test.Seconds), budgetText(test.Budget)))
	}
	return lines
}

// OKLine is the one line a run inside budget prints, naming the single slowest
// package overall. With no packages at all the slot is "none", not empty.
func (r Report) OKLine() string {
	if r.Packages == 0 || r.Slowest.Name == "" {
		return "CI-SLOW OK packages=0 slowest=none"
	}
	return fmt.Sprintf("CI-SLOW OK packages=%d slowest=%s:%s",
		r.Packages, oneline.Field(r.Slowest.Name), Seconds(r.Slowest.Seconds))
}

// slowestList renders a package's slowest tests, comma-separated. A package
// whose total is over budget but which carries no test-level row (a fixture
// with only the package event) still names at least one row: the package
// itself, so the line is never `slowest=`.
func slowestList(pkg Package) string {
	if len(pkg.Slowest) == 0 {
		return oneline.Field(pkg.Name) + ":" + Seconds(pkg.Seconds)
	}
	parts := make([]string, 0, len(pkg.Slowest))
	for _, test := range pkg.Slowest {
		parts = append(parts, oneline.Field(test.Name)+":"+Seconds(test.Seconds))
	}
	return strings.Join(parts, ",")
}
