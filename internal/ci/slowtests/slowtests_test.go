package slowtests

import (
	"fmt"
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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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

// The unit tier's budgets (Glenn 2026-09-26 11:20 AM ET: "unit tests be < 2s
// (ideally <1)"): a package over two seconds and a top-level test over one are
// each a finding, and an allowlist row raises exactly the budget it names.
func TestSlowTestsJudgesPackagesAndTestsAgainstTheirRows(t *testing.T) {
	t.Parallel()

	rows, err := ParseAllowlist(strings.NewReader("# pkg\ttest\tseconds\tmeasured\n\ninternal/ci\t-\t17.1\t11.4s@run1\ninternal/ci\tTestBig\t11.1\t7.4s@run1\n"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := `{"Action":"pass","Package":"example.com/m/internal/ci","Test":"TestBig","Elapsed":7.4}
{"Action":"pass","Package":"example.com/m/internal/ci","Test":"TestSmall","Elapsed":1.2}
{"Action":"pass","Package":"example.com/m/internal/ci","Test":"TestSmall/sub","Elapsed":1.1}
{"Action":"pass","Package":"example.com/m/internal/ci","Elapsed":11.4}
{"Action":"pass","Package":"example.com/m/cmd/fast","Test":"TestA","Elapsed":0.4}
{"Action":"pass","Package":"example.com/m/cmd/fast","Elapsed":2.5}
`
	report := Judge(slowEvents(t, fixture), Budgets{Package: 2, Test: 1, Rows: rows})
	if got, want := report.ExitCode(), 2; got != want {
		t.Errorf("ExitCode = %d, want %d", got, want)
	}
	want := []string{
		"CI-SLOW package=example.com/m/cmd/fast seconds=2.5s budget=2s slowest=TestA:0.4s",
		"CI-SLOW test=TestSmall package=example.com/m/internal/ci seconds=1.2s budget=1s",
	}
	if got := report.OverLines(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("OverLines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A malformed allowlist row, a budget that is not positive, and a row written
// twice are refused with the line named, never read as "no budget".
func TestSlowTestsAllowlistRefusesABadRow(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"internal/ci TestA 2 1.5s@run1\n",
		"internal/ci\tTestA\t0\t1.5s@run1\n",
		"internal/ci\tTestA\tfast\t1.5s@run1\n",
		"internal/ci\tTestA\t2\t1.5s@run1\ninternal/ci\tTestA\t3\t1.5s@run1\n",
	} {
		if _, err := ParseAllowlist(strings.NewReader(text)); err == nil || !strings.Contains(err.Error(), "line ") {
			t.Errorf("ParseAllowlist(%q) = %v, want an error naming the line", text, err)
		}
	}
}

// PROBE 4: a row that does not name its measurement -- the three-column shape
// the list had before, a time with no place, a place with no time, a time that
// is not a number -- is refused with its line, and so is a budget under its own
// measurement or more than MaxHeadroom times it. A measured row is read with
// both halves kept.
func TestSlowTestsAllowlistRefusesARowWithoutItsMeasurement(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"internal/ci\tTestA\t2\n",
		"internal/ci\tTestA\t2\t1.5s\n",
		"internal/ci\tTestA\t2\t@run1\n",
		"internal/ci\tTestA\t2\t1.5@run1\n",
		"internal/ci\tTestA\t2\tfasts@run1\n",
		"internal/ci\tTestA\t2\t0s@run1\n",
		"internal/ci\tTestA\t1.4\t1.5s@run1\n",
		"internal/ci\tTestA\t4.6\t1.5s@run1\n",
	} {
		if _, err := ParseAllowlist(strings.NewReader(text)); err == nil || !strings.Contains(err.Error(), "line 1") {
			t.Errorf("ParseAllowlist(%q) = %v, want a refusal naming line 1", text, err)
		}
	}
	rows, err := ParseAllowlist(strings.NewReader("internal/ci\tTestA\t1.5\t0.99s@space\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Seconds != 1.5 || rows[0].Measured != 0.99 || rows[0].Where != "space" {
		t.Errorf("rows = %+v, want one row: budget 1.5, measured 0.99 at space", rows)
	}
}

// PROBE 4 of the #4413 ruling: where a row was measured is a CI run
// (`run<id>`) or a bench, never free text. `2s@guess` is refused, and so are a
// run with no id or a non-numeric one and a bench name with a suffix;
// `2s@run36264290984` and `2s@space` are read.
func TestSlowTestsMeasuredWhereIsARunOrABench(t *testing.T) {
	t.Parallel()

	for _, where := range []string{"guess", "run", "runabc", "run36264290984x", "idle-2026-09-26", "studio-load12-2026-09-26", "Space"} {
		text := "internal/ci\tTestA\t2\t2s@" + where + "\n"
		if _, err := ParseAllowlist(strings.NewReader(text)); err == nil || !strings.Contains(err.Error(), "neither run<id>") {
			t.Errorf("ParseAllowlist(%q) = %v, want a refusal: %q is neither a run nor a bench", text, err, where)
		}
	}
	for _, where := range []string{"run36264290984", "space", "studio"} {
		text := "internal/ci\tTestA\t2\t2s@" + where + "\n"
		rows, err := ParseAllowlist(strings.NewReader(text))
		if err != nil || len(rows) != 1 || rows[0].Where != where {
			t.Errorf("ParseAllowlist(%q) = %+v, %v; want one row measured at %s", text, rows, err, where)
		}
	}
}

// PROBE 6: a package over its budget whose every test is under its own (many
// small tests, the shape of cmd/nova-swarm's 245) is one package line naming
// its top three tests by time, and no test line.
func TestSlowTestsManySmallTestsNameThePackageAndItsTopThree(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "{\"Action\":\"pass\",\"Package\":\"example.com/m/cmd/many\",\"Test\":\"TestN%02d\",\"Elapsed\":0.%02d}\n", i, 10+i)
	}
	b.WriteString(`{"Action":"pass","Package":"example.com/m/cmd/many","Elapsed":3.1}` + "\n")
	report := Judge(slowEvents(t, b.String()), Budgets{Package: 2, Test: 1})
	want := "CI-SLOW package=example.com/m/cmd/many seconds=3.1s budget=2s slowest=TestN29:0.4s,TestN28:0.4s,TestN27:0.4s"
	if got := report.OverLines(); len(got) != 1 || got[0] != want {
		t.Errorf("OverLines = %q, want only %q", got, want)
	}
	if report.ExitCode() != 2 {
		t.Errorf("ExitCode = %d, want 2", report.ExitCode())
	}
}

// PROBE 1 and 6 at the verdict (the #4413 ruling: a budget verdict is the
// same on any machine). The same go test -json fixture -- a 1.4 s test, and
// cmd/nova-bus at 58.8 s as a package with no row (its darwin time in run
// 36264290984) -- gives the same exit at load 2 and at load 20: on a pull
// request or self-hosted leg (enforce false) every CI-SLOW line and the CI-LOAD
// line are printed and the exit is 0; on the nightly leg (enforce true) the
// 1.4 s test is red over its row (0.4 s measured, 1.2 s budget, three times
// it) and cmd/nova-bus is red at the 2 s default. An unledgered SLEEPS skip is
// red on both legs at both loads.
func TestSlowTestsVerdictIsTheSameAtAnyLoad(t *testing.T) {
	t.Parallel()

	rows, err := ParseAllowlist(strings.NewReader("m/busy\tTestSlow\t1.2\t0.4s@run36264290984\n"))
	if err != nil {
		t.Fatal(err)
	}
	b := Budgets{Package: 2, Test: 1, Rows: rows}
	timeOnly := `{"Action":"pass","Package":"example.com/m/busy","Test":"TestSlow","Elapsed":1.4}
{"Action":"pass","Package":"example.com/m/busy","Elapsed":1.5}
{"Action":"pass","Package":"github.com/mas-bandwidth/nova-tools/cmd/nova-bus","Test":"TestWait","Elapsed":0.9}
{"Action":"pass","Package":"github.com/mas-bandwidth/nova-tools/cmd/nova-bus","Elapsed":58.8}
`
	sleeps := `{"Action":"output","Package":"example.com/m/busy","Test":"TestSleeps","Output":"    x_test.go:3: SLEEPS: needs a mocked clock\n"}
{"Action":"skip","Package":"example.com/m/busy","Test":"TestSleeps","Elapsed":0}
{"Action":"pass","Package":"example.com/m/busy","Elapsed":0.1}
`
	slowLines := []string{
		"CI-SLOW package=github.com/mas-bandwidth/nova-tools/cmd/nova-bus seconds=58.8s budget=2s slowest=TestWait:0.9s",
		"CI-SLOW test=TestSlow package=example.com/m/busy seconds=1.4s budget=1.2s",
	}
	const sleepsLine = "CI-SLEEPS test=TestSleeps package=example.com/m/busy: skipped for a wall-clock wait and not on ledger.txt; inject a clock or tag it //go:build functional"
	loads := map[string]Load{
		"load 2":  {Avg: 2, CPUs: 32, Known: true},
		"load 20": {Avg: 20, CPUs: 32, Known: true},
		"unread":  {CPUs: 32, Why: "sysctl -n vm.loadavg: executable file not found in $PATH"},
	}
	for name, load := range loads {
		for _, leg := range []struct {
			name    string
			enforce bool
			code    int
		}{{"pull request", false, 0}, {"nightly", true, 2}} {
			lines, code := Verdict(Judge(slowEvents(t, timeOnly), b), load, leg.enforce, "ledger.txt")
			want := append(append([]string{}, slowLines...), load.LoadLine())
			if code != leg.code || strings.Join(lines, "\n") != strings.Join(want, "\n") {
				t.Errorf("%s, %s leg: exit %d lines\n%s\nwant exit %d lines\n%s", name, leg.name, code, strings.Join(lines, "\n"), leg.code, strings.Join(want, "\n"))
			}
			lines, code = Verdict(Judge(slowEvents(t, sleeps), b), load, leg.enforce, "ledger.txt")
			if code != 2 || !strings.Contains(strings.Join(lines, "\n"), sleepsLine) {
				t.Errorf("%s, %s leg, a SLEEPS skip: exit %d lines\n%s\nwant exit 2 with %q", name, leg.name, code, strings.Join(lines, "\n"), sleepsLine)
			}
		}
	}
	if got, want := loads["load 20"].LoadLine(), "CI-LOAD load=20.00 cpus=32 per-cpu=0.62: measured, not a verdict"; got != want {
		t.Errorf("LoadLine = %q, want %q", got, want)
	}
	if got, want := loads["unread"].LoadLine(), "CI-LOAD load=unknown cpus=32: measured, not a verdict (the load could not be read: sysctl -n vm.loadavg: executable file not found in $PATH)"; got != want {
		t.Errorf("LoadLine = %q, want %q", got, want)
	}
}

// A SLEEPS skip the ledger names is not a finding; a subtest's SLEEPS skip is
// its top-level test's, reported once; a test that prints the marker and
// passes is not a skip.
func TestSlowTestsSleepsLedgerExemptsItsRowsOnly(t *testing.T) {
	t.Parallel()

	ledger, err := ParseSleeps(strings.NewReader("# pkg\ttest\twhere\nm/known\tTestKnown\t#4221\n"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := `{"Action":"output","Package":"example.com/m/known","Test":"TestKnown","Output":"SLEEPS: x\n"}
{"Action":"skip","Package":"example.com/m/known","Test":"TestKnown","Elapsed":0}
{"Action":"output","Package":"example.com/m/new","Test":"TestNew/a","Output":"SLEEPS: x\n"}
{"Action":"skip","Package":"example.com/m/new","Test":"TestNew/a","Elapsed":0}
{"Action":"output","Package":"example.com/m/new","Test":"TestNew/b","Output":"SLEEPS: x\n"}
{"Action":"skip","Package":"example.com/m/new","Test":"TestNew/b","Elapsed":0}
{"Action":"output","Package":"example.com/m/new","Test":"TestSaysIt","Output":"SLEEPS: printed, not skipped\n"}
{"Action":"pass","Package":"example.com/m/new","Test":"TestSaysIt","Elapsed":0.1}
`
	report := Judge(slowEvents(t, fixture), Budgets{Package: 2, Test: 1, Sleeps: ledger})
	if len(report.Sleepers) != 1 || report.Sleepers[0] != (Sleeper{Package: "example.com/m/new", Name: "TestNew"}) {
		t.Errorf("Sleepers = %+v, want only example.com/m/new TestNew", report.Sleepers)
	}
	for _, bad := range []string{"m\tTestA\n", "m\tTestA/sub\twhere\n", "m\tTestA\tw\nm\tTestA\tw\n"} {
		if _, err := ParseSleeps(strings.NewReader(bad)); err == nil || !strings.Contains(err.Error(), "line ") {
			t.Errorf("ParseSleeps(%q) = %v, want an error naming the line", bad, err)
		}
	}
}
