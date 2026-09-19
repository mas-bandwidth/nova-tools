package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// The onboarding standard (ONBOARDING.md), pinned for this binary. A newcomer's
// first stumble is the spec for these tests: the usage banner's examples are
// RUN rather than read, every refusal a first run hits must say what the flag
// WANTS, and the transcript in TESTS.md is compared against what the tool
// actually prints. Guidance nothing checks rots into a claim about a message
// that has since moved.

// localize points an example or a transcript command at this test's own
// directories, so what is under test is the command's SHAPE and not the
// reader's directory layout. ./wake.state is a file the tool WRITES, so it has
// to land somewhere this test owns.
func localize(t *testing.T, args []string) []string {
	t.Helper()
	tmp := t.TempDir()
	out := append([]string(nil), args...)
	for i, a := range out {
		switch a {
		case "./reports":
			out[i] = exampleReports
		case "./wake.state":
			out[i] = filepath.Join(tmp, "wake.state")
		}
	}
	return out
}

func examples(t *testing.T) []string {
	t.Helper()
	// The banner is asked for, because a bare invocation no longer IS one: a
	// refusal costs one line and names the door. Reading it through that door
	// is also a test that the door opens.
	r := wakeRun(t, "help")
	if r.exit != 0 {
		t.Fatalf("`nova-wake help` must print the usage and exit 0, got %d; stderr: %s", r.exit, r.stderr)
	}
	lines, err := onboarding.ExampleLines(r.stdout, "nova-wake")
	if err != nil {
		t.Fatalf("%s\n\n%s", err, r.stdout)
	}
	return lines
}

// (a) The usage ends in an `example:` block of lines that actually run. They
// are run here against the fixture: an example that has drifted out of the flag
// set teaches the wrong invocation to exactly the reader who cannot tell.
func TestUsageBannerExamplesRun(t *testing.T) {
	for _, ex := range examples(t) {
		r := wakeRun(t, localize(t, strings.Fields(ex)[1:])...)
		if r.exit == 2 {
			t.Errorf("the usage example %q does not run: exit 2 (could not run)\nstderr: %s", ex, r.stderr)
			continue
		}
		if r.exit != 0 {
			t.Errorf("the usage example %q ran but exited %d; an example a stranger types should pass on the fixture\nstderr: %s", ex, r.exit, r.stderr)
		}
		if r.stdout == "" {
			t.Errorf("the usage example %q printed nothing on stdout", ex)
		}
	}
}

func TestQuickstartIsTheFirstThingTheBannerOffers(t *testing.T) {
	exs := examples(t)
	if !strings.HasPrefix(exs[0], "nova-wake quickstart ") {
		t.Errorf("the first example is %q; a first run should be offered quickstart first", exs[0])
	}
	if !strings.Contains(usage, "nova-wake quickstart --state <file>") {
		t.Error("the usage block does not list the quickstart verb")
	}
}

// A bare invocation costs ONE line and names the door, and the banner is behind
// that door on stdout at exit 0. A flag typo used to cost between 1,900 and
// 6,500 bytes of banner to say that a dash was in the wrong place, and a
// harness reading a tool's stderr pays that on every typo.
func TestABareInvocationIsOneLineAndNamesTheDoor(t *testing.T) {
	r := wakeRun(t)
	if r.exit != 2 {
		t.Errorf("a bare nova-wake exits %d, want 2", r.exit)
	}
	if r.stdout != "" {
		t.Errorf("a refusal belongs on stderr, got %q on stdout", r.stdout)
	}
	lines := strings.Split(strings.TrimSuffix(r.stderr, "\n"), "\n")
	if len(lines) > 2 {
		t.Errorf("a bare nova-wake printed %d lines, want 1 (or 2 with its hint):\n%s", len(lines), r.stderr)
	}
	if !strings.Contains(r.stderr, "run: nova-wake help") {
		t.Errorf("a bare nova-wake names no door:\n%s", r.stderr)
	}
	if help := wakeRun(t, "help"); help.exit != 0 || help.stdout == "" || help.stderr != "" {
		t.Errorf("`nova-wake help` must put the banner on stdout at exit 0; exit %d, stderr %q", help.exit, help.stderr)
	}
	if bad := wakeRun(t, "wathc"); bad.exit != 2 || !strings.Contains(bad.stderr, "run: nova-wake help") {
		t.Errorf("an unknown verb must cost one line and name the door; exit %d:\n%s", bad.exit, bad.stderr)
	}
}

// (b) A refusal says what the flag or input WANTS, not only what was wrong.
func TestARefusalSaysWhatTheFlagWants(t *testing.T) {
	state := filepath.Join(t.TempDir(), "wake.state")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"watch with no state", []string{"watch", "--max", "5s", "--on-deadline", "x", "--interval", "5s", "--reports", exampleReports}, stateHint},
		{"watch with no deadline", []string{"watch", "--state", state, "--on-deadline", "x", "--interval", "5s", "--reports", exampleReports}, maxHint},
		{"watch with no default action", []string{"watch", "--state", state, "--max", "5s", "--interval", "5s", "--reports", exampleReports}, deadlineHint},
		{"watch with no interval", []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "x", "--reports", exampleReports}, intervalHint},
		{"an entry with no cadence", []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "x", "--interval", "5s", "--entry", "a/b#1"}, entryEveryHint},
		{"a bus with no name", []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "x", "--interval", "5s", "--bus", "."}, asHint},
		{"no source at all", []string{"watch", "--state", state, "--max", "5s", "--on-deadline", "x", "--interval", "5s"}, sourceHint},
		{"serve with no command", []string{"serve", "--bus", ".", "--as", "Rowan", "--interval", "30s", "--state", state, "--hours", "2"}, onNoteHint},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := wakeRun(t, tc.args...)
			if r.exit != 2 {
				t.Fatalf("exit = %d, want 2 -- guidance must not soften the refusal; stderr: %s", r.exit, r.stderr)
			}
			if !strings.Contains(r.stderr, tc.want) {
				t.Errorf("stderr = %q,\nwant it to contain the hint %q", r.stderr, tc.want)
			}
			if r.stdout != "" {
				t.Errorf("a refusal must print nothing on stdout, got %q", r.stdout)
			}
		})
	}
}

// (b), the other half: one run reports every problem it can find. Being sent
// back a second time for something the first run could already see is the
// stumble this pins shut.
func TestIndependentProblemsAreReportedInOneRun(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"watch with nothing at all", []string{"watch"},
			[]string{"--state is required", "--max is required", "--on-deadline is required", "--interval is required", "no source named"}},
		{"a bus with neither name nor word count", []string{"watch", "--state", "s", "--max", "5s", "--on-deadline", "x", "--interval", "5s", "--bus", "."},
			[]string{"--as is required", "--receipt-max-words is required"}},
		{"serve with nothing at all", []string{"serve"},
			[]string{"--bus is required", "--as is required", "--state is required", "--on-note is required"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := wakeRun(t, tc.args...)
			if r.exit != 2 {
				t.Fatalf("exit = %d, want 2; stderr: %s", r.exit, r.stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(r.stderr, want) {
					t.Errorf("one run must name every problem it can find; %q is missing from:\n%s", want, r.stderr)
				}
			}
		})
	}
}

// (c) The `### First run` transcript of docs/TESTS.md is EXECUTED: every
// documented command is run, in order, in one directory, and its whole output
// is compared with the block written under it -- same number of lines, same
// lines, same order.
//
// WHAT THIS REPLACES. The old test reduced every line to its field NAMES with a
// local `shapeOf`, and said in as many words that "numbers, paths, stamps and
// tails are a run's own business". Under that comparison
// `WAKE CHANGE after=0s polls=1 ... reports=2 ...` and
// `WAKE CHANGE after=9h polls=97 ... reports=0 ...` are the same line, a
// dropped line removes a lookup rather than an assertion, and the two REPORT
// lines could be in either order. For a tool whose whole answer is the numbers,
// that left the transcript checking its own punctuation.
//
// NOTHING IS NORMALISED, and the fake clock is why. This package already runs
// the binary on wake.Fake; handing it the instant the transcript was recorded
// at makes `at=2026-09-11T18:56:43Z` reproduce exactly, and makes the second
// command -- `--max 5s` -- return at once instead of costing five seconds of
// wall clock on every CI leg.
//
// The paths are typed as written. `./reports` and `./wake.state` are what a
// reader types and what the tool PRINTS BACK on `state=` and on every REPORT
// line, so the test runs in a directory of its own with the fixture under the
// documented name, rather than rewriting the paths -- which is what the old
// `localize` did, and it gave each command a DIFFERENT temp directory, so the
// `watch` never saw the state the `quickstart` had just written.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs(exampleReports)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-wake")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-wake", lines)
	if err != nil {
		t.Fatal(err)
	}
	// The sitting is the two forms a first run has: the quickstart that chooses
	// the flags for you and returns, and the watch that was given them. A block
	// that has quietly lost one of the two is short of a first run in a way no
	// per-line comparison would say, because the lines that remain match.
	verbs := map[string]bool{}
	for _, s := range steps {
		verbs[s.Args[0]] = true
	}
	for _, verb := range []string{"quickstart", "watch"} {
		if !verbs[verb] {
			t.Errorf("the `### First run` block never runs `nova-wake %s`; the first sitting is both", verb)
		}
	}

	dir := t.TempDir()
	copyTree(t, fixture, filepath.Join(dir, "reports"))
	t.Chdir(dir)
	for _, p := range onboarding.Execute(steps, documented(t)) {
		t.Error(p)
	}
}

// transcriptAt is the instant the `### First run` block was recorded at, which
// the fake clock is started from so that `at=` is the documented stamp. It is
// spelled here rather than parsed out of the document because a test that read
// its own expected value from the file it is checking would agree with anything.
var transcriptAt = time.Date(2026, 9, 11, 18, 56, 43, 0, time.UTC)

// documented runs one line of the transcript on that clock.
func documented(t *testing.T) onboarding.Runner {
	t.Helper()
	return func(s onboarding.Step) (onboarding.Result, error) {
		if s.Stdin != "" {
			return onboarding.Result{}, errReadsNothing
		}
		r := wakeRunAt(t, transcriptAt, s.Args...)
		return onboarding.Result{Code: r.exit, Stdout: r.stdout, Stderr: r.stderr}, nil
	}
}

type readsNothing struct{}

func (readsNothing) Error() string {
	return "nova-wake reads no stdin; a `< path` in its transcript is the document's bug"
}

var errReadsNothing = readsNothing{}

// copyTree copies the fixture reports to where the transcript says they are.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(to, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			copyTree(t, filepath.Join(from, e.Name()), filepath.Join(to, e.Name()))
			continue
		}
		body, err := os.ReadFile(filepath.Join(from, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to, e.Name()), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
