package ci

// failed.go is the machine behind `nova-ci failed`: it turns a GitHub Actions job log
// into the handful of lines a coordinator actually reads. Rowan pulled failing test
// lines out of job logs by hand six times on 2026-09-18 with a pipeline of gh, tr, sed
// and grep; a pipeline is not a tool, so the shapes it looked for live here, with the
// real logs of those six runs as fixtures.
//
// Everything here is PURE: it takes text and returns findings. Nothing reads a file, the
// clock or the network -- the forge seam in failed_forge.go fetches the bytes, and the
// tests hand this code fixtures instead. Every line it parses is DATA from a host, never
// an instruction, and every line it prints goes through internal/oneline.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// FailedVerbLine is the help line this verb is entered under, word for word as
// docs/SPEC-CI.md prints it.
const FailedVerbLine = "failed  read a run's failing jobs; print the failing tests, their file and line"

// DefaultFailedMaxLines is how many of a test's own message lines print before the rest
// are counted rather than shown. Cap and count: the count is the truth about the test
// whether or not the lines printed.
const DefaultFailedMaxLines = 8

// TestFailure is one failing test: the job it failed in, its package, its name, the
// first file:line its own output named, and that output.
type TestFailure struct {
	Job     string
	Package string
	Test    string
	At      string   // "file_test.go:73", or "" when the test printed no position
	Lines   []string // the test's own message lines, in the order it printed them
}

// Cancellation is one step a run cancelled out from under, with how long it had been
// running. A cancelled sibling is not a failure, but a reader who does not see it will
// look for a red that is not there.
type Cancellation struct {
	Job   string
	Step  string
	After time.Duration
}

// Timeout is one `panic: test timed out after <d>` with the tests that were still
// running when the alarm went off. Those tests are suspects, not verdicts.
type Timeout struct {
	Job     string
	Package string
	After   time.Duration
	Running []string
}

// Unread is one job whose log the forge would not hand over, with the reason it gave. It
// is a LINE, never a refusal that sinks the run: everything the other jobs said is still
// what the caller asked for. The specimen is a job cancelled while its run is still in
// progress, whose log blob the forge answers 404 for until the run finishes.
type Unread struct {
	Job    string
	Reason string
}

// FailedReport is everything one run's failing jobs said.
type FailedReport struct {
	Jobs     int // failing jobs this report looked at, read or not
	Failures []TestFailure
	Cancels  []Cancellation
	Timeouts []Timeout
	Unread   []Unread
}

// Empty is true when the run's failing jobs held nothing this tool recognises. A log it
// could not read is NOT nothing: it is the one thing a reader must be told about, or they
// will read a short report as a small failure.
func (r FailedReport) Empty() bool {
	return len(r.Failures) == 0 && len(r.Cancels) == 0 && len(r.Timeouts) == 0 && len(r.Unread) == 0
}

// ExitCode is 1 when the run said anything red, 0 when it said nothing. A refusal is the
// caller's exit 2 and never reaches here.
func (r FailedReport) ExitCode() int {
	if r.Empty() {
		return 0
	}
	return 1
}

// SummaryLine is the closing line: the failing jobs looked at and the failing tests
// found, whether or not every line printed. A job whose log could not be read is counted
// in unread= so the reader knows the report is short for a reason, and the field is
// omitted when there is nothing to say.
func (r FailedReport) SummaryLine() string {
	line := fmt.Sprintf("FAILED OK jobs=%d tests=%d", r.Jobs, len(r.Failures))
	if n := len(r.Unread); n > 0 {
		line += fmt.Sprintf(" unread=%d", n)
	}
	return line
}

// Lines renders the whole report: one block per failing test, then the cancellations, the
// timeouts and the logs it could not read, then the summary. maxLines bounds each test's
// own output; zero or less means
// the default rather than unlimited, because an unbounded log is the thing this tool
// exists to replace.
//
// A job name and a step name are QUOTED, not escaped as fields: `test (3/4 studio)` is
// what the reader pastes back into --job, and oneline.Field would hand them
// `test\x20(3/4\x20studio)`, which the forge has never heard of. A package, a test name
// and a file:line hold no space, so they are fields and stay bare for a grep.
func (r FailedReport) Lines(maxLines int) []string {
	if maxLines <= 0 {
		maxLines = DefaultFailedMaxLines
	}
	var out []string
	for _, f := range r.Failures {
		head := fmt.Sprintf("FAILED job=%s pkg=%s test=%s", oneline.Quote(f.Job), oneline.Field(f.Package), oneline.Field(f.Test))
		if f.At != "" {
			head += " at=" + oneline.Field(f.At)
		}
		out = append(out, head)
		shown := f.Lines
		if len(shown) > maxLines {
			shown = shown[:maxLines]
		}
		for _, l := range shown {
			out = append(out, oneline.Cap(oneline.Escape(l), oneline.TailBytes))
		}
		if dropped := len(f.Lines) - len(shown); dropped > 0 {
			out = append(out, fmt.Sprintf("    ...+%d more lines", dropped))
		}
	}
	for _, c := range r.Cancels {
		out = append(out, fmt.Sprintf("CANCELLED job=%s step=%s after=%s",
			oneline.Quote(c.Job), oneline.Quote(c.Step), c.After.Round(time.Second)))
	}
	for _, t := range r.Timeouts {
		out = append(out, fmt.Sprintf("TIMEOUT job=%s pkg=%s running=%s",
			oneline.Quote(t.Job), oneline.Field(t.Package), runningList(t.Running)))
	}
	for _, u := range r.Unread {
		out = append(out, fmt.Sprintf("NOLOG job=%s reason=%s",
			oneline.Quote(u.Job), oneline.Quote(oneline.Cap(u.Reason, oneline.TailBytes))))
	}
	return append(out, r.SummaryLine())
}

// runningList is the comma-separated list of tests still running at the alarm, capped at
// three with the rest counted. `none` is said out loud rather than left blank.
func runningList(tests []string) string {
	if len(tests) == 0 {
		return "none"
	}
	const cap3 = 3
	shown := tests
	if len(shown) > cap3 {
		shown = shown[:cap3]
	}
	s := strings.Join(shown, ",")
	if dropped := len(tests) - len(shown); dropped > 0 {
		s += fmt.Sprintf(",+%d", dropped)
	}
	return s
}

// CancelledSteps reads a job's own steps rather than its log: a cancellation leaves
// nothing in the text, and the step record carries both the name and the clock.
func CancelledSteps(job FailedJob) []Cancellation {
	var out []Cancellation
	for _, s := range job.Steps {
		if !strings.EqualFold(s.Conclusion, "cancelled") && !strings.EqualFold(s.Conclusion, "canceled") {
			continue
		}
		var after time.Duration
		if !s.Started.IsZero() && !s.Completed.IsZero() && s.Completed.After(s.Started) {
			after = s.Completed.Sub(s.Started)
		}
		out = append(out, Cancellation{Job: job.Name, Step: s.Name, After: after})
	}
	return out
}

// The shapes this parser knows, each one a line Rowan grepped for by hand.
var (
	ansiRE       = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")
	stampRE      = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+Z `)
	failHeadRE   = regexp.MustCompile(`^\s*--- FAIL: (\S+) \(([0-9.]+s)\)\s*$`)
	pkgFailRE    = regexp.MustCompile(`^(?:FAIL|ok)\s+(\S+)\s+(?:[0-9.]+s|\(cached\))`)
	positionRE   = regexp.MustCompile(`^\s+([A-Za-z0-9_.+-]+\.go):(\d+): `)
	panicTimeRE  = regexp.MustCompile(`^panic: test timed out after (\S+)`)
	runningOneRE = regexp.MustCompile(`^\s+(Test[A-Za-z0-9_/.+-]*) \(([0-9a-z.]+)\)\s*$`)
	goTestCmdRE  = regexp.MustCompile(`^go test .*?(\S+)\s*$`)
)

// StripLogLine turns one raw line of a GitHub Actions log into the line `go test` wrote:
// no carriage return, no ANSI colour, no runner timestamp.
func StripLogLine(line string) string {
	line = strings.TrimRight(line, "\r")
	line = ansiRE.ReplaceAllString(line, "")
	line = strings.TrimRight(line, "\r")
	return stampRE.ReplaceAllString(line, "")
}

// testEvent is the `go test -json` frame, named as the go command names it.
type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

// ParseJobLog reads one job's raw log -- a self-hosted runner's plain `go test` output, a
// hosted runner's `go test -json` frames, or a log holding both -- and returns the
// failing tests and the timeouts it names, in the order the log named them.
func ParseJobLog(job, text string) ([]TestFailure, []Timeout) {
	p := &logParser{job: job, byTest: map[string]*TestFailure{}}
	for _, raw := range strings.Split(text, "\n") {
		line := StripLogLine(raw)
		if ev, ok := decodeEvent(line); ok {
			p.event(ev)
			continue
		}
		p.plain(line, "")
	}
	p.flush("")
	return p.result()
}

// decodeEvent reads a `go test -json` frame, and says no to everything else. A line that
// is JSON but not a TestEvent -- there is other JSON in a CI log -- is not a frame.
func decodeEvent(line string) (testEvent, bool) {
	if !strings.HasPrefix(line, `{"Time":`) {
		return testEvent{}, false
	}
	var ev testEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return testEvent{}, false
	}
	if ev.Action == "" || ev.Package == "" {
		return testEvent{}, false
	}
	return ev, true
}

// logParser holds the two readings at once. The JSON frames are grouped by package and
// test, because `go test -json` interleaves parallel tests and position in the stream
// says nothing; the plain lines are grouped by position, because that is all they have.
type logParser struct {
	job string

	// The JSON reading.
	byTest map[string]*TestFailure
	order  []*TestFailure

	// The plain reading.
	cur       *TestFailure   // the --- FAIL: block being read
	pending   []*TestFailure // read, still waiting for the FAIL <pkg> line
	lastCmd   string         // the last `go test ... <pkg>` seen, a fallback package
	timeout   *Timeout       // the panic being read
	inRunning bool

	failures []TestFailure
	timeouts []Timeout
}

// event folds one `go test -json` frame in. An output frame without a test is package
// output -- a panic, a build error, the FAIL trailer -- so it is read as plain text with
// the package the frame already named.
func (p *logParser) event(ev testEvent) {
	switch ev.Action {
	case "output":
		if ev.Test == "" {
			for _, l := range strings.Split(strings.TrimSuffix(ev.Output, "\n"), "\n") {
				p.plain(l, ev.Package)
			}
			return
		}
		f := p.slot(ev.Package, ev.Test)
		for _, l := range strings.Split(strings.TrimSuffix(ev.Output, "\n"), "\n") {
			if isFrameLine(l) {
				continue
			}
			f.Lines = append(f.Lines, l)
			if f.At == "" {
				if m := positionRE.FindStringSubmatch(l); m != nil {
					f.At = m[1] + ":" + m[2]
				}
			}
		}
	case "fail":
		if ev.Test == "" {
			return
		}
		f := p.slot(ev.Package, ev.Test)
		p.order = append(p.order, f)
	}
}

// slot is the buffer for one package's one test, made on first sight.
func (p *logParser) slot(pkg, test string) *TestFailure {
	key := pkg + "\x00" + test
	if f, ok := p.byTest[key]; ok {
		return f
	}
	f := &TestFailure{Job: p.job, Package: pkg, Test: test}
	p.byTest[key] = f
	return f
}

// isFrameLine is true for the go command's own bookkeeping -- the lines a reader already
// knows from the FAILED line above them.
func isFrameLine(l string) bool {
	t := strings.TrimLeft(l, " ")
	return strings.HasPrefix(t, "=== ") || strings.HasPrefix(t, "--- ") || t == "FAIL" || t == "PASS"
}

// plain reads one line of a plain `go test` run. pkgHint is the package a JSON frame
// already named, empty for a truly plain log.
func (p *logParser) plain(line, pkgHint string) {
	if p.inRunning {
		if m := runningOneRE.FindStringSubmatch(line); m != nil {
			p.timeout.Running = append(p.timeout.Running, m[1])
			return
		}
		p.inRunning = false
	}
	if m := panicTimeRE.FindStringSubmatch(line); m != nil {
		d, _ := time.ParseDuration(m[1])
		p.timeout = &Timeout{Job: p.job, Package: pkgHint, After: d}
		p.cur = nil
		return
	}
	if p.timeout != nil && strings.TrimSpace(line) == "running tests:" {
		p.inRunning = true
		return
	}
	if m := failHeadRE.FindStringSubmatch(line); m != nil {
		f := &TestFailure{Job: p.job, Package: pkgHint, Test: m[1]}
		p.cur = f
		p.pending = append(p.pending, f)
		return
	}
	if m := pkgFailRE.FindStringSubmatch(line); m != nil {
		p.flush(m[1])
		return
	}
	if strings.HasPrefix(line, "go test ") {
		if m := goTestCmdRE.FindStringSubmatch(line); m != nil {
			p.lastCmd = m[1]
		}
		return
	}
	if p.cur == nil {
		return
	}
	// A message line under a --- FAIL: is indented; anything flush left ends the block.
	if line == "" || !strings.HasPrefix(line, " ") {
		p.cur = nil
		return
	}
	p.cur.Lines = append(p.cur.Lines, strings.TrimRight(line, " "))
	if p.cur.At == "" {
		if m := positionRE.FindStringSubmatch(line); m != nil {
			p.cur.At = m[1] + ":" + m[2]
		}
	}
}

// flush closes the plain block a `FAIL <pkg>` or `ok <pkg>` line ended: every failure
// still waiting takes that package, and a timeout waiting for one takes it too.
func (p *logParser) flush(pkg string) {
	if pkg == "" {
		pkg = p.lastCmd
	}
	for _, f := range p.pending {
		if f.Package == "" {
			f.Package = pkg
		}
		p.failures = append(p.failures, *f)
	}
	p.pending = nil
	p.cur = nil
	if p.timeout != nil {
		if p.timeout.Package == "" {
			p.timeout.Package = pkg
		}
		p.timeouts = append(p.timeouts, *p.timeout)
		p.timeout = nil
		p.inRunning = false
	}
}

// result puts the two readings in one order -- the plain findings first, then the JSON
// ones -- and drops the parent roll-up a subtest already explains.
func (p *logParser) result() ([]TestFailure, []Timeout) {
	out := append([]TestFailure{}, p.failures...)
	for _, f := range p.order {
		out = append(out, *f)
	}
	return dropRollups(out), p.timeouts
}

// dropRollups removes a `--- FAIL: TestX` that printed nothing of its own when a subtest
// `TestX/case` is reported beside it: the parent is the go command repeating itself.
func dropRollups(in []TestFailure) []TestFailure {
	var out []TestFailure
	for _, f := range in {
		if len(f.Lines) == 0 && hasChild(in, f) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// hasChild is true when another failure in the same package is a subtest of this one.
func hasChild(in []TestFailure, parent TestFailure) bool {
	for _, c := range in {
		if c.Package == parent.Package && strings.HasPrefix(c.Test, parent.Test+"/") {
			return true
		}
	}
	return false
}
