package card

// wrapper_spec.go is the spec gate (nova-tools#4313). Glenn 2026-09-26 ~11:05
// AM ET, the quality lens: "how can I make the quality of the friends+swarm
// work as good as, or better than software built yourself?" The contrast with
// the day's child builds: the worker ran the touched tests before pushing and
// the change carried its red test. The card is a spec, and the wrapper holds
// a DONE code card to it between the commit step and the push:
//
//   - the class test: the card's TEST line names one test (`<package>
//     <TestName>`, cardhdr.ParseTest); the diff adds or changes at least one
//     _test.go file; the wrapper runs TEST at HEAD (it passes: GREEN) and at
//     BASE with the diff's test files checked out over it (it fails: RED). A
//     card whose TEST is `none <why>` is excused from the class test, not from
//     CI, and the why is on the card, in RESULT.md and in the PR body, so the
//     reader sees it. A bare `none` or no TEST line is refused: the reader
//     must see why.
//   - CI's own answer: `nova-ci local --base <base>` in the checkout (#4360:
//     exactly the unit tier CI runs for the diff, on the touched packages, at
//     -p 2, niced). Exit 1 is a red, and its RED lines are the names. Where
//     the verb cannot run (not on PATH, or a repository with no CI selection
//     to match, its exit 2), the wrapper runs the touched packages itself,
//     `go test -json -p 2 -count=1`, and reads the red names from the events.
//
// A red refuses the push: the copy ends FAILED with the typed reason
// (no-test, test-not-green, test-not-red, ci-red), the why names the red and
// the remedy, and the rows, red names included, go under `## Gates` in
// RESULT.md. No PR is opened from a red. Every child process runs through
// the wrapper's Runner (execRun), beating the card's lease while it runs, so
// the unit tests drive the gate with a fake and start nothing.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci/slowtests"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// The gate's typed reasons: the end's reason word of a refused card.
const (
	GateNoTest   = "no-test"        // no TEST, a bare none, or no test file in the diff
	GateNotGreen = "test-not-green" // TEST fails at HEAD
	GateNotRed   = "test-not-red"   // TEST passes at BASE with the diff's tests
	GateCIRed    = "ci-red"         // nova-ci local (or the touched packages) red
	GatePass     = "pass"
)

// GateInput is what the gate runs on.
type GateInput struct {
	// Repo is the commit step's checkout, <job>/out/repo, at Head.
	Repo string
	// Base is the sha repo/ was staged at: the card's base-sha, or the PR's
	// head for a fix copy. Head is the commit the step made.
	Base, Head string
	// Test is the card's TEST line.
	Test string
	// Paths are the files the commit changed (ChangedPaths(Repo, Base)).
	Paths []string
	// Timeout bounds each command; zero is DefaultCheckTimeout. Ticks and
	// Beat keep the lease live while a command runs.
	Timeout time.Duration
	Ticks   <-chan time.Time
	Beat    func()
	// Run is the Runner; nil is execRun.
	Run Runner
}

// GateResult is what the gate found.
type GateResult struct {
	// Reason is GatePass, or the typed reason the card is refused for.
	Reason string
	// Why is the refusal, one line with its remedy; "" on pass.
	Why string
	// Reds are the red names as nova-ci local prints them:
	// `RED package=<p> test=<t>`.
	Reds []string
	// Rows are the Gates rows, in order: TEST at head, TEST at base, CI.
	Rows []string
	// Check is TEST at head (pass, fail, not-run); Green its GREEN line and
	// Red the RED line (TEST failing at base), for the typed record.
	Check, Green, Red string
}

// Passed reports a green gate.
func (r GateResult) Passed() bool { return r.Reason == GatePass }

// Line is the gate's receipt: `gate=<pass|reason> red=<names|->`.
func (r GateResult) Line() string {
	red := "-"
	if len(r.Reds) > 0 {
		red = strings.Join(r.Reds, ";")
	}
	return "gate=" + r.Reason + " red=" + red
}

// nice is how every test process the gate starts is started: the bench's
// real work comes first (nova-ci local nices itself the same way).
var nice = []string{"nice", "-n", "15"}

// RunSpecGate holds a DONE code card to its spec (the file comment).
func RunSpecGate(ctx context.Context, in GateInput) GateResult {
	run := in.Run
	if run == nil {
		run = execRun
	}
	res := GateResult{Reason: GatePass, Check: "not-run"}
	refuse := func(reason, why string) GateResult {
		res.Reason, res.Why = reason, oneField(why)
		return res
	}
	tl, why := cardhdr.ParseTest(in.Test)
	if why != "" {
		res.Rows = append(res.Rows, "TEST: refused ("+why+")")
		return refuse(GateNoTest, why)
	}
	if tl.None {
		res.Rows = append(res.Rows, "TEST: none ("+tl.Why+"); no class test required")
	} else {
		tests := testFiles(in.Repo, in.Paths)
		if len(tests) == 0 {
			res.Rows = append(res.Rows, "TEST "+in.Test+": not-run (the diff adds or changes no test file)")
			return refuse(GateNoTest, "the diff adds or changes no test file: add or change one test that fails at base-sha "+short(in.Base)+" and passes at your head, the one TEST names ("+in.Test+")")
		}
		head := runCheck(ctx, run, in.Repo, in.Test, in.Timeout, in.Ticks, in.Beat)
		res.Check, res.Green = head.Check, head.Green
		res.Rows = append(res.Rows, "TEST "+in.Test+" at head "+short(in.Head)+": "+strings.TrimPrefix(head.Gate, head.Cmd+": "))
		if head.Check != "pass" {
			return refuse(GateNotGreen, "TEST "+in.Test+" is not green at head "+short(in.Head)+": "+head.Gate+"; make it pass before you commit")
		}
		red, stage := runAtBase(ctx, run, in, tests)
		if stage != "" {
			res.Rows = append(res.Rows, "TEST "+in.Test+" at base "+short(in.Base)+": not-run ("+stage+")")
			return refuse(GateNotRed, "TEST "+in.Test+" could not run at base-sha "+short(in.Base)+": "+stage)
		}
		res.Rows = append(res.Rows, "TEST "+in.Test+" at base "+short(in.Base)+" with the diff's tests: "+strings.TrimPrefix(red.Gate, red.Cmd+": "))
		if red.Check != "fail" {
			return refuse(GateNotRed, "TEST "+in.Test+" passes at base-sha "+short(in.Base)+" with your test files ("+strings.Join(tests, " ")+"): the test does not fail without the change; write one that does")
		}
		res.Red = red.Gate
	}
	rows, reds, reason, why := runCI(ctx, run, in)
	res.Rows = append(res.Rows, rows...)
	res.Reds = reds
	if reason != "" {
		return refuse(reason, why)
	}
	return res
}

// testFiles are the changed paths that are Go test files still present in
// repo (a deleted test cannot be checked out over base).
func testFiles(repo string, paths []string) []string {
	var out []string
	for _, p := range paths {
		if !strings.HasSuffix(p, "_test.go") {
			continue
		}
		if fi, err := os.Stat(filepath.Join(repo, filepath.FromSlash(p))); err == nil && fi.Mode().IsRegular() {
			out = append(out, p)
		}
	}
	return out
}

// runAtBase stages Base in a worktree beside repo (<out>/base), checks the
// diff's test files out of Head over it and runs TEST there. stage names the
// step that could not run; the worktree is removed either way.
func runAtBase(ctx context.Context, run Runner, in GateInput, tests []string) (CheckRun, string) {
	base := filepath.Join(filepath.Dir(in.Repo), "base")
	git := func(dir string, argv ...string) string {
		var out tailBuffer
		argv = append([]string{"git", "-c", "core.hooksPath=/dev/null"}, argv...)
		exit, err := run(ctx, Cmd{Dir: dir, Argv: argv, Out: &out, Timeout: in.Timeout, Ticks: in.Ticks, Beat: in.Beat})
		switch {
		case err != nil:
			return "git " + strings.Join(argv[3:], " ") + ": " + oneField(err.Error())
		case exit != 0:
			return fmt.Sprintf("git %s: exit %d: %s", strings.Join(argv[3:], " "), exit, out.tail(2))
		}
		return ""
	}
	out := filepath.Dir(in.Repo)
	_ = safepath.RemoveUnder(out, base)
	if why := git(in.Repo, "worktree", "add", "--detach", "--force", base, in.Base); why != "" {
		return CheckRun{}, why
	}
	defer func() {
		_ = git(in.Repo, "worktree", "remove", "--force", base)
		_ = safepath.RemoveUnder(out, base)
		_ = git(in.Repo, "worktree", "prune")
	}()
	if why := git(base, append([]string{"checkout", in.Head, "--"}, tests...)...); why != "" {
		return CheckRun{}, why
	}
	return runCheck(ctx, run, base, in.Test, in.Timeout, in.Ticks, in.Beat), ""
}

// redLineMark is how nova-ci local prints a red test.
const redLineMark = "RED package="

// runCI is CI's answer for the diff: nova-ci local, else the touched packages.
// It returns the rows, the red names, and the reason and why of a refusal.
func runCI(ctx context.Context, run Runner, in GateInput) (rows, reds []string, reason, why string) {
	var out lineBuffer
	exit, err := run(ctx, Cmd{Dir: in.Repo, Argv: []string{"nova-ci", "local", "--base", in.Base}, Out: &out,
		Timeout: in.Timeout, Ticks: in.Ticks, Beat: in.Beat})
	for _, l := range out.lines {
		if strings.HasPrefix(l, redLineMark) {
			reds = append(reds, l)
		}
	}
	summary := out.last("nova-ci local: packages=")
	switch {
	case err == nil && exit == 0:
		if summary == "" {
			summary = "nova-ci local: green"
		}
		return []string{summary}, nil, "", ""
	case err == nil && exit == 1:
		if summary == "" {
			summary = "nova-ci local: red"
		}
		rows = append([]string{summary}, reds...)
		if len(reds) == 0 {
			reds = []string{"RED package=- test=- (nova-ci local exit 1 with no RED line: " + out.tail(1) + ")"}
			rows = append(rows, reds...)
		}
		return rows, reds, GateCIRed, "nova-ci local red: " + strings.Join(reds, "; ") + "; fix the red, never the budget"
	case err == context.DeadlineExceeded:
		return []string{"nova-ci local: fail timeout after " + in.Timeout.String()}, nil, GateCIRed, "nova-ci local did not finish in " + in.Timeout.String() + ": a red at the cap is your defect to fix"
	}
	// The verb could not run here (not on PATH, or a repository with no CI
	// selection to match): the touched packages, the way its refusal says.
	could := out.tail(1)
	if err != nil {
		could = oneField(err.Error())
	}
	pkgs := goPackages(in.Paths)
	if len(pkgs) == 0 {
		return []string{"nova-ci local: not-run (" + could + "); no Go package touched"}, nil, "", ""
	}
	argv := append(append([]string{}, nice...), "go", "test", "-json", "-p", "2", "-count=1")
	argv = append(argv, pkgs...)
	var ev lineBuffer
	exit, err = run(ctx, Cmd{Dir: in.Repo, Argv: argv, Out: &ev, Timeout: in.Timeout, Ticks: in.Ticks, Beat: in.Beat})
	cmdline := "go test -p 2 -count=1 " + strings.Join(pkgs, " ")
	reds = redEvents(ev.lines)
	switch {
	case err == context.DeadlineExceeded:
		return []string{"nova-ci local: not-run (" + could + ")", cmdline + ": fail timeout after " + in.Timeout.String()}, nil,
			GateCIRed, cmdline + " did not finish in " + in.Timeout.String() + ": a red at the cap is your defect to fix"
	case err != nil:
		return []string{"nova-ci local: not-run (" + could + ")", cmdline + ": not-run (" + oneField(err.Error()) + ")"}, nil,
			GateCIRed, "CI could not run for the diff: nova-ci local " + could + "; " + cmdline + ": " + oneField(err.Error())
	case exit == 0:
		return []string{"nova-ci local: not-run (" + could + ")", cmdline + ": pass"}, nil, "", ""
	}
	if len(reds) == 0 {
		reds = []string{"RED package=- test=- (" + cmdline + " exit " + fmt.Sprint(exit) + ": " + ev.tail(1) + ")"}
	}
	rows = append([]string{"nova-ci local: not-run (" + could + ")", cmdline + ": red"}, reds...)
	return rows, reds, GateCIRed, cmdline + " red: " + strings.Join(reds, "; ") + "; fix the red, never the budget"
}

// goPackages are the ./-relative packages of the changed Go files, sorted.
func goPackages(paths []string) []string {
	seen := map[string]bool{}
	for _, p := range paths {
		if !strings.HasSuffix(p, ".go") {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(p))
		if dir == "." {
			seen["."] = true
			continue
		}
		seen["./"+dir] = true
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// redEvents reads a `go test -json` stream: a failed test is `RED
// package=<p> test=<t>`; a package that failed with no failed test (a build
// error, a panic, TestMain) is `test=-`, once.
func redEvents(lines []string) []string {
	var reds []string
	failed := map[string]bool{}
	var pkgFail []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || t[0] != '{' {
			continue
		}
		var ev slowtests.Event
		if err := json.Unmarshal([]byte(t), &ev); err != nil || ev.Package == "" {
			continue
		}
		switch {
		case ev.Action == "fail" && ev.Test != "":
			reds = append(reds, "RED package="+oneField(ev.Package)+" test="+oneField(ev.Test))
			failed[ev.Package] = true
		case (ev.Action == "fail" || ev.Action == "build-fail") && ev.Test == "":
			pkgFail = append(pkgFail, ev.Package)
		}
	}
	for _, p := range pkgFail {
		if !failed[p] {
			failed[p] = true
			reds = append(reds, "RED package="+oneField(p)+" test=- (the package failed outside a test: a build error, a panic, TestMain or the -timeout)")
		}
	}
	return reds
}

// lineBuffer keeps a command's output as lines (the last 4096 of them).
type lineBuffer struct {
	buf   []byte
	lines []string
}

func (l *lineBuffer) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		l.add(string(l.buf[:i]))
		l.buf = l.buf[i+1:]
	}
}

func (l *lineBuffer) add(s string) {
	if s = strings.TrimRight(s, "\r"); strings.TrimSpace(s) == "" {
		return
	}
	l.lines = append(l.lines, s)
	if len(l.lines) > 4096 {
		l.lines = l.lines[len(l.lines)-4096:]
	}
}

// last is the last line with prefix, or "".
func (l *lineBuffer) last(prefix string) string {
	if len(l.buf) > 0 {
		l.add(string(l.buf))
		l.buf = nil
	}
	for i := len(l.lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(l.lines[i], prefix) {
			return oneField(l.lines[i])
		}
	}
	return ""
}

// tail is the last n lines, joined with " | ".
func (l *lineBuffer) tail(n int) string {
	if len(l.buf) > 0 {
		l.add(string(l.buf))
		l.buf = nil
	}
	rows := l.lines
	if len(rows) > n {
		rows = rows[len(rows)-n:]
	}
	return oneField(strings.Join(rows, " | "))
}

// AppendGates writes the gate's rows under `## Gates` at the end of
// <out>/RESULT.md, the model's file, before the wrapper copies it out: the
// reader sees the red names beside the model's word. A card that wrote no
// RESULT.md gets none (the record says w_result=absent).
func AppendGates(out string, rows []string) error {
	path := filepath.Join(out, "RESULT.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var b strings.Builder
	b.Write(raw)
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("## Gates\n")
	for _, r := range rows {
		b.WriteString("- " + oneField(r) + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
