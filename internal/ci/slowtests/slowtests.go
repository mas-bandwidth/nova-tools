// Package slowtests is the machine behind cmd/nova-ci's `slowtests` verb in
// docs/SPEC-CI.md, "The per-package test time budget". It reads the
// newline-delimited TestEvent objects `go test -json` writes, sums the
// package-level Elapsed for each package, keeps the slowest few test-level rows
// so a finding can name where the time went, and reports every package whose
// total is over the budget. The one file it reads is ci.yml, which names the
// runners a measured row may name, and it never touches the network or the
// clock: the events and the budget come from the caller, so a test feeds canned
// lines and asserts a verdict.
package slowtests

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"gopkg.in/yaml.v3"
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
	Sleepers  []Sleeper
	Slowest   Package
	Budget    time.Duration
}

// Budgets is what Judge holds a run to. Package is the default seconds for a
// package's total; Test is the default seconds for one top-level test, zero
// meaning tests are not judged one by one. Rows are the allowlist: a row whose
// Test is empty is a package's own budget, any other row one test's. Sleeps is
// the ledger of the tests already skipped with the SLEEPS marker; a SLEEPS skip
// not on it is a finding whatever the budgets or the load.
type Budgets struct {
	Package float64
	Test    float64
	Rows    []Row
	Sleeps  []SleepRow
}

// Row is one allowlist line: `pkg<TAB>test<TAB>seconds<TAB>measured`, with `-`
// in the test column for a package's own row. Package is module-relative
// (internal/ci) and matches an event's import path by its trailing path
// elements. Seconds is the budget the row enforces; Measured is the time the row
// was cut from and Where the CI run (`run<id>`) or the bench label Benches
// reads from ci.yml that measured it, so no number on the list is a guess: the
// measured column is `<seconds>s@<where>`, and the budget may not exceed
// MaxHeadroom times the measurement.
type Row struct {
	Package  string
	Test     string
	Seconds  float64
	Measured float64
	Where    string
}

// MaxHeadroom is the most a row's budget may exceed its own measurement: three
// times. The same tests take about 1.0-1.2 s on a busy runner against about
// 0.9 s idle, well inside it.
const MaxHeadroom = 3.0

// SleepRow is one line of the SLEEPS ledger: `pkg<TAB>test<TAB>where`, a
// top-level test that skips itself with the SLEEPS marker because it waits on
// the wall clock, and the run or issue that found the wait.
type SleepRow struct {
	Package string
	Test    string
	Where   string
}

// SleepsMarker is the text a unit test's t.Skip starts with when it is skipped
// for a sleep or a wall-clock wait; go test -json carries it as an output event
// of the skipped test.
const SleepsMarker = "SLEEPS:"

// Sleeper is one test skipped with the SLEEPS marker that the ledger does not
// name.
type Sleeper struct {
	Package string
	Name    string
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

// ParseAllowlist reads `pkg<TAB>test<TAB>seconds<TAB>measured` rows. Blank
// lines and lines starting with # are skipped; `-` in the test column is the
// package's own row. The measured column is `<seconds>s@<where>`: the time the
// row was cut from and where it was measured, a CI run or a runner label
// ciBenches reads from ci.yml. A malformed row, a budget that is not a
// positive number, a row with no measurement, a budget under its measurement
// or over MaxHeadroom times it, or a row written twice is an error naming its
// 1-based line.
func ParseAllowlist(r io.Reader) ([]Row, error) {
	benches, err := ciBenches()
	if err != nil {
		return nil, err
	}
	return parseAllowlist(r, benches)
}

// parseAllowlist reads the rows and holds each measured where to benches, so a
// caller that has ci.yml's own text judges the rows against that text and
// reads no file itself (docs/STANDARD.md section 5: a model where there is a
// machine).
func parseAllowlist(r io.Reader, benches []string) ([]Row, error) {
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
		if len(f) != 4 || f[0] == "" || f[1] == "" {
			return nil, fmt.Errorf("line %d: want pkg<TAB>test<TAB>seconds<TAB><measured>s@<where>, got %q", line, text)
		}
		secs, err := strconv.ParseFloat(f[2], 64)
		if err != nil || secs <= 0 {
			return nil, fmt.Errorf("line %d: budget %q is not a positive number of seconds", line, f[2])
		}
		measured, where, err := parseMeasured(f[3], benches)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if secs < measured || secs > MaxHeadroom*measured+1e-9 {
			return nil, fmt.Errorf("line %d: budget %gs is not between its measurement %gs and %g times it", line, secs, measured, MaxHeadroom)
		}
		row := Row{Package: f[0], Test: f[1], Seconds: secs, Measured: measured, Where: where}
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

// ciYMLPath is the workflow the bench labels are read from, module-relative:
// the one home of the runner inventory, which docs/STANDARD.md section 4 holds
// to one place and never a second.
const ciYMLPath = ".github/workflows/ci.yml"

// groupFlag matches the flag a ci.yml step names a runner group with
// (`--linux-group <label>`): a group is a runs-on label the legs carry, and a
// leg dealt by a matrix writes it nowhere else.
var groupFlag = regexp.MustCompile(`--[a-z]+-group (\S+)`)

// runsOnLabel is the machine label of one `runs-on` value: the last of a
// self-hosted list, the labels before it being the platform's own
// (self-hosted, the OS, the arch). A hosted `runs-on: <string>` and a list
// whose last label is a `${{ }}` expression name no machine of their own.
func runsOnLabel(key, value *yaml.Node) string {
	items := value.Content
	if key.Value != "runs-on" || len(items) < 2 || items[0].Value != "self-hosted" {
		return ""
	}
	return items[len(items)-1].Value
}

// Benches reads the labels a row may name as where it was measured from ci.yml's
// own text, so the inventory has the one home ci.yml is (docs/STANDARD.md
// section 4): the machine label of every self-hosted `runs-on` list and every
// runner group a step's groupFlag names, in the order the file names them and
// once each. An expression is no label: it names the matrix that fills it in.
func Benches(ciYML []byte) ([]string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(ciYML, &doc); err != nil {
		return nil, err
	}
	var labels []string
	add := func(label string) {
		if label != "" && !strings.HasPrefix(label, "${") && !slices.Contains(labels, label) {
			labels = append(labels, label)
		}
	}
	var walk func(node *yaml.Node)
	walk = func(node *yaml.Node) {
		for i, child := range node.Content {
			if node.Kind == yaml.MappingNode && i%2 == 1 {
				add(runsOnLabel(node.Content[i-1], child))
			}
			if child.Kind == yaml.ScalarNode {
				for _, match := range groupFlag.FindAllStringSubmatch(child.Value, -1) {
					add(match[1])
				}
			}
			walk(child)
		}
	}
	walk(&doc)
	return labels, nil
}

// ciBenches reads ciYMLPath from the checkout the working directory is in,
// walking up to the directory that holds it, and returns the labels Benches
// reads from that text. A tree with no ci.yml has no bench labels: a row then
// names a CI run, and a bench label is refused with the file named.
var ciBenches = sync.OnceValues(func() ([]string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, nil
	}
	for {
		if text, readErr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(ciYMLPath))); readErr == nil {
			return Benches(text)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil
		}
		dir = parent
	}
})

// parseMeasured reads `<seconds>s@<where>`: a positive time, and where is a
// CI run (`run<id>`, the digits of a GitHub Actions run id) or one of benches,
// the labels Benches reads from ci.yml. Free text is refused: `2s@guess` names
// nothing a reader can open.
func parseMeasured(field string, benches []string) (float64, string, error) {
	before, where, found := strings.Cut(field, "s@")
	if !found || before == "" || where == "" {
		return 0, "", fmt.Errorf("measured %q is not <seconds>s@<where>; a row names the time it was cut from and where", field)
	}
	secs, err := strconv.ParseFloat(before, 64)
	if err != nil || secs <= 0 {
		return 0, "", fmt.Errorf("measured %q is not a positive number of seconds", field)
	}
	if !measuredWhere(where, benches) {
		return 0, "", fmt.Errorf("measured %q: where %q is neither run<id> (a CI run) nor a runner label %s names (%s)", field, where, ciYMLPath, strings.Join(benches, ", "))
	}
	return secs, where, nil
}

// measuredWhere reports whether where is `run<digits>` or one of benches.
func measuredWhere(where string, benches []string) bool {
	if id, ok := strings.CutPrefix(where, "run"); ok && id != "" {
		for _, c := range id {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}
	return slices.Contains(benches, where)
}

// ParseSleeps reads the SLEEPS ledger: `pkg<TAB>test<TAB>where` rows, blank
// lines and # comments skipped. A malformed row or a row written twice is an
// error naming its 1-based line.
func ParseSleeps(r io.Reader) ([]SleepRow, error) {
	sc := bufio.NewScanner(r)
	var rows []SleepRow
	seen := map[string]int{}
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		f := strings.Split(text, "\t")
		if len(f) != 3 || f[0] == "" || f[1] == "" || f[2] == "" || strings.Contains(f[1], "/") {
			return nil, fmt.Errorf("line %d: want pkg<TAB>TopLevelTest<TAB>where, got %q", line, text)
		}
		key := f[0] + "\t" + f[1]
		if first, dup := seen[key]; dup {
			return nil, fmt.Errorf("line %d: %s %s is already on line %d", line, f[0], f[1], first)
		}
		seen[key] = line
		rows = append(rows, SleepRow{Package: f[0], Test: f[1], Where: f[2]})
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

// ledgered reports whether the SLEEPS ledger names (pkg, test).
func (b Budgets) ledgered(pkg, test string) bool {
	return slices.ContainsFunc(b.Sleeps, func(row SleepRow) bool { return row.Test == test && matches(row.Package, pkg) })
}

// topLevel is a test name's top-level test: a subtest's skip is its parent's.
func topLevel(test string) string {
	if i := strings.Index(test, "/"); i >= 0 {
		return test[:i]
	}
	return test
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
// output never depends on map iteration. A test that skips with the SLEEPS
// marker in its output and is not on b.Sleeps is a Sleeper, in first-seen
// order, one per top-level test.
func Judge(events []Event, b Budgets) Report {
	byPackage := map[string]*Package{}
	var order []string
	var overTests []OverTest
	var sleepers []Sleeper
	marked := map[string]bool{}
	sleeperSeen := map[string]bool{}
	for _, ev := range events {
		if ev.Package != "" && ev.Test != "" && ev.Action == "output" && strings.Contains(ev.Output, SleepsMarker) {
			marked[ev.Package+"\t"+ev.Test] = true
		}
		if ev.Package != "" && ev.Test != "" && ev.Action == "skip" && marked[ev.Package+"\t"+ev.Test] {
			key := ev.Package + "\t" + topLevel(ev.Test)
			if !sleeperSeen[key] && !b.ledgered(ev.Package, topLevel(ev.Test)) {
				sleeperSeen[key] = true
				sleepers = append(sleepers, Sleeper{Package: ev.Package, Name: topLevel(ev.Test)})
			}
		}
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

	report := Report{Packages: len(order), Budget: time.Duration(b.Package * float64(time.Second)), OverTests: overTests, Sleepers: sleepers}
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

// ExitCode is the enforced verdict (the nightly leg's): 1, the check ran and
// said no, when any package or test is over budget or any test is an
// unledgered SLEEPS skip, 0 when none is; 2 is left to a run that could not
// read its input. Verdict is what a leg exits with.
func (r Report) ExitCode() int {
	if len(r.Over) > 0 || len(r.OverTests) > 0 || len(r.Sleepers) > 0 {
		return 1
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

// SleepsLines is one line per unledgered SLEEPS skip. It is red on every leg: a
// test skipped for a wall-clock wait is what the test does, not how busy the
// runner was.
func (r Report) SleepsLines(ledger string) []string {
	lines := make([]string, 0, len(r.Sleepers))
	for _, s := range r.Sleepers {
		lines = append(lines, fmt.Sprintf("CI-SLEEPS test=%s package=%s: skipped for a wall-clock wait and not on %s; inject a clock or tag it //go:build functional",
			oneline.Field(s.Name), oneline.Field(s.Package), oneline.Field(ledger)))
	}
	return lines
}

// Load is the host's run-queue load average and its logical CPU count when a
// run is judged. It is a MEASUREMENT printed beside the times, never an input
// to the verdict: a budget verdict is the same on any machine, so the load
// only tells a reader what the box was doing while the times were taken. Known
// is false when the host has no load average to read (Windows) or the read
// failed; Why then says so.
type Load struct {
	Avg   float64
	CPUs  int
	Known bool
	Why   string
}

// PerCPU is the load over the CPUs, or 0 when unknown.
func (l Load) PerCPU() float64 {
	if !l.Known || l.CPUs <= 0 {
		return 0
	}
	return l.Avg / float64(l.CPUs)
}

// LoadLine is the CI-LOAD line every run prints: the load the times were
// taken at, or why it is unknown. It carries no verdict.
func (l Load) LoadLine() string {
	if !l.Known || l.CPUs <= 0 {
		why := l.Why
		if why == "" {
			why = "no CPU count"
		}
		return fmt.Sprintf("CI-LOAD load=unknown cpus=%d: measured, not a verdict (the load could not be read: %s)", l.CPUs, oneline.Escape(why))
	}
	return fmt.Sprintf("CI-LOAD load=%s cpus=%d per-cpu=%s: measured, not a verdict",
		strconv.FormatFloat(l.Avg, 'f', 2, 64), l.CPUs, strconv.FormatFloat(l.PerCPU(), 'f', 2, 64))
}

// Verdict is what the check prints and its exit code. Every CI-SLOW line and
// the CI-LOAD line are printed on every leg, so the time is always measured.
// The exit code never reads the load, and a no is 1 (the check ran and said
// no), apart from the 2 of a run that could not read its input:
//
//   - an unledgered SLEEPS skip (a CI-SLEEPS line) is 1 on every leg: it is
//     what the test does, a static fact;
//   - a CI-SLOW line is 1 only when enforce is set, which one caller does: the
//     nightly whole-tree run on the idle reference leg (ci.yml's schedule
//     branch of the test step, `make test SLOWTESTS_ENFORCE=1`). Everywhere
//     else it is a printed measurement and exit 0.
func Verdict(r Report, load Load, enforce bool, ledger string) ([]string, int) {
	var lines []string
	slow := r.OverLines()
	lines = append(lines, slow...)
	sleeps := r.SleepsLines(ledger)
	lines = append(lines, sleeps...)
	if len(slow) == 0 && len(sleeps) == 0 {
		lines = append(lines, r.OKLine())
	}
	lines = append(lines, load.LoadLine())
	code := 0
	if len(sleeps) > 0 || (enforce && len(slow) > 0) {
		code = 1
	}
	return lines, code
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
