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
// Elapsed, while a test-level event carries that one test's Elapsed.
type Event struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
}

// Test is one test-level row kept for a package's slowest list.
type Test struct {
	Name    string
	Seconds float64
}

// Package is one package's summed elapsed time and its slowest tests, sorted
// worst first and capped at testsKept.
type Package struct {
	Name    string
	Seconds float64
	Slowest []Test
}

// Report is one run of the budget check: how many packages were seen, the ones
// over budget (worst first), the single slowest package overall, and the budget
// they were judged against (kept only so a finding can print it).
type Report struct {
	Packages int
	Over     []Package
	Slowest  Package
	Budget   time.Duration
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

// Sum folds the events into a report. A package's total is the sum of its
// package-level Elapsed (Test == ""); a test-level event only feeds the slowest
// list. Over-budget packages are returned worst first, ties by name, so the
// output never depends on map iteration.
func Sum(events []Event, budget time.Duration) Report {
	byPackage := map[string]*Package{}
	var order []string
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
	}

	report := Report{Packages: len(order), Budget: budget}
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
		if pkg.Seconds > budget.Seconds() {
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

// ExitCode is 2 when any package is over budget, 0 when none is.
func (r Report) ExitCode() int {
	if len(r.Over) > 0 {
		return 2
	}
	return 0
}

// OverLines is one line per over-budget package, worst first.
func (r Report) OverLines() []string {
	lines := make([]string, 0, len(r.Over))
	for _, pkg := range r.Over {
		lines = append(lines, fmt.Sprintf("CI-SLOW package=%s seconds=%s budget=%ds slowest=%s",
			oneline.Field(pkg.Name), Seconds(pkg.Seconds), int64(r.Budget.Seconds()), slowestList(pkg)))
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
