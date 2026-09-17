package slowtests

import (
	"strings"
	"testing"
	"time"
)

// slowtests_test.go is the red-test contract of the `slowtests` verb the CI
// test step runs (docs/SPEC-CI.md, "The per-package test time budget"). Every
// case below feeds canned `go test -json` TestEvent lines through Parse and
// Sum; no test here runs `go test`, touches the network, or reads a real
// timing. The fixtures use fractional seconds so the rendering is pinned down
// to the printed shape.

// slowBudget is the budget every case below is judged against, the same sixty
// the workflow passes; a case may pass its own to pin a boundary.
const slowBudget = 60 * time.Second

// slowEvents parses a fixture and fails the test on a parse error, so each
// summing case reads as events in, verdict out.
func slowEvents(t *testing.T, fixture string) []Event {
	t.Helper()
	events, err := Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("Parse(%q): %v", fixture, err)
	}
	return events
}

// A package under budget is a green with the single slowest package named.
func TestSlowTestsUnderBudgetIsOK(t *testing.T) {
	fixture := `{"Action":"run","Package":"example.com/pkg","Test":"TestA"}
{"Action":"pass","Package":"example.com/pkg","Test":"TestA","Elapsed":3.2}
{"Action":"pass","Package":"example.com/pkg","Elapsed":3.2}
`
	report := Sum(slowEvents(t, fixture), slowBudget)
	if got, want := report.ExitCode(), 0; got != want {
		t.Errorf("ExitCode = %d, want %d (nothing is over budget)", got, want)
	}
	if got, want := len(report.Over), 0; got != want {
		t.Errorf("Over = %d packages, want %d", got, want)
	}
	want := "CI-SLOW OK packages=1 slowest=example.com/pkg:3.2s"
	if got := report.OKLine(); got != want {
		t.Errorf("OKLine = %q, want %q", got, want)
	}
}

// A package over budget is one finding naming the package, its total, the
// budget and its slowest tests, worst first.
func TestSlowTestsOverBudgetNamesThePackageAndSlowestTests(t *testing.T) {
	fixture := `{"Action":"pass","Package":"example.com/pkg","Test":"TestB","Elapsed":2.9}
{"Action":"pass","Package":"example.com/pkg","Test":"TestA","Elapsed":3.2}
{"Action":"pass","Package":"example.com/pkg","Elapsed":75.3}
`
	report := Sum(slowEvents(t, fixture), slowBudget)
	if got, want := report.ExitCode(), 2; got != want {
		t.Errorf("ExitCode = %d, want %d (a package is over budget)", got, want)
	}
	lines := report.OverLines()
	if len(lines) != 1 {
		t.Fatalf("OverLines = %d lines, want 1: %v", len(lines), lines)
	}
	want := "CI-SLOW package=example.com/pkg seconds=75.3s budget=60s slowest=TestA:3.2s,TestB:2.9s"
	if lines[0] != want {
		t.Errorf("OverLines[0] = %q, want %q", lines[0], want)
	}
}

// An empty stdin is not a package over budget; it is zero packages, and the
// OK line says so rather than leaving the slowest slot empty.
func TestSlowTestsEmptyInputIsOKWithZeroPackages(t *testing.T) {
	report := Sum(nil, slowBudget)
	if report.Packages != 0 {
		t.Errorf("Packages = %d, want 0", report.Packages)
	}
	if got, want := report.ExitCode(), 0; got != want {
		t.Errorf("ExitCode = %d, want %d", got, want)
	}
	want := "CI-SLOW OK packages=0 slowest=none"
	if got := report.OKLine(); got != want {
		t.Errorf("OKLine = %q, want %q", got, want)
	}
}

// A line that is not a TestEvent is a refusal naming the line, never a silent
// skip: a truncated pipe must not read as a clean run.
func TestSlowTestsMalformedLineIsRefused(t *testing.T) {
	fixture := `{"Action":"pass","Package":"example.com/pkg","Elapsed":3.2}
this is not json
`
	_, err := Parse(strings.NewReader(fixture))
	if err == nil {
		t.Fatal("a malformed line parsed without an error")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error = %q, want it to name line 2", err.Error())
	}
}

// The slowest list holds the slowest few tests, sorted, and never grows past
// the cap; the package total is what decides over budget, not the test rows.
func TestSlowTestsSlowestListIsSortedAndCapped(t *testing.T) {
	fixture := `{"Action":"pass","Package":"example.com/pkg","Test":"TestD","Elapsed":1.0}
{"Action":"pass","Package":"example.com/pkg","Test":"TestC","Elapsed":1.5}
{"Action":"pass","Package":"example.com/pkg","Test":"TestB","Elapsed":2.0}
{"Action":"pass","Package":"example.com/pkg","Test":"TestA","Elapsed":9.0}
{"Action":"pass","Package":"example.com/pkg","Elapsed":70.0}
`
	report := Sum(slowEvents(t, fixture), slowBudget)
	if len(report.Over) != 1 {
		t.Fatalf("Over = %d packages, want 1", len(report.Over))
	}
	slowest := report.Over[0].Slowest
	if len(slowest) != 3 {
		t.Fatalf("slowest tests kept = %d, want the cap of 3: %v", len(slowest), slowest)
	}
	got := report.OverLines()[0]
	want := "CI-SLOW package=example.com/pkg seconds=70.0s budget=60s slowest=TestA:9.0s,TestB:2.0s,TestC:1.5s"
	if got != want {
		t.Errorf("OverLines[0] = %q, want %q", got, want)
	}
}

// More than one package over budget prints one line each, worst first, and the
// order does not depend on map iteration.
func TestSlowTestsOverPackagesAreOrderedWorstFirst(t *testing.T) {
	fixture := `{"Action":"pass","Package":"example.com/small","Elapsed":61.0}
{"Action":"pass","Package":"example.com/big","Elapsed":90.0}
{"Action":"pass","Package":"example.com/under","Elapsed":5.0}
`
	report := Sum(slowEvents(t, fixture), slowBudget)
	lines := report.OverLines()
	if len(lines) != 2 {
		t.Fatalf("OverLines = %d lines, want 2: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "package=example.com/big") {
		t.Errorf("first over line = %q, want the worst package first", lines[0])
	}
	if !strings.Contains(lines[1], "package=example.com/small") {
		t.Errorf("second over line = %q, want the smaller offender second", lines[1])
	}
}
