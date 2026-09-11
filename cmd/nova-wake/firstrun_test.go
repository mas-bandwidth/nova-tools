package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// (c) The transcript in TESTS.md, checked against the tool. The commands in it
// are run here against the fixture, and every transcript line must match a line
// the tool actually printed -- the event prefix and the field NAMES, in order.
// Numbers, paths, stamps and tails are a run's own business and are
// deliberately not compared: pinning those would make the transcript a fixture
// instead of a document.
func TestTheFirstRunTranscriptMatchesWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-wake")
	if err != nil {
		t.Fatal(err)
	}
	var printed map[string]bool
	seen := map[string]int{}
	for _, line := range lines {
		if cmd, ok := strings.CutPrefix(line, "$ nova-wake "); ok {
			r := wakeRun(t, localize(t, strings.Fields(cmd))...)
			if r.exit == 2 {
				t.Fatalf("the transcript command %q does not run: exit 2, stderr: %s", line, r.stderr)
			}
			printed = map[string]bool{}
			for _, out := range strings.Split(r.stdout, "\n") {
				if s := shapeOf(out); s != "" {
					printed[s] = true
				}
			}
			continue
		}
		s := shapeOf(line)
		if s == "" {
			continue
		}
		if printed == nil {
			t.Fatalf("transcript line before any command: %q", line)
		}
		if !printed[s] {
			t.Errorf("the transcript line\n  %s\nhas shape %q, which this tool never prints. Re-run the command and paste what it said.", line, s)
		}
		seen[strings.Fields(s)[0]+" "+secondWord(s)]++
	}
	for prefix, want := range map[string]int{"WAKE REPORT": 2, "WAKE CHANGE": 1, "WAKE QUIET": 1} {
		if seen[prefix] != want {
			t.Errorf("the First run transcript shows %d %s lines, want %d", seen[prefix], prefix, want)
		}
	}
}

// shapeOf is onboarding.Shape's rule with ONE difference, and the difference is
// this tool's grammar rather than a loosening. onboarding.Shape keeps the
// second token whole, because every other binary here prints one of OK, FAIL or
// NOTE there. nova-wake's opening line puts a FIELD in that slot --
// `WAKE at=<stamp> ...`, which docs/SPEC-WAKE.md's grammar fixes -- so keeping
// it whole would pin a wall-clock stamp into the transcript and make the
// document a fixture. Every token carrying an "=" reduces to its key here,
// wherever it sits.
func shapeOf(line string) string {
	head := line
	if i := strings.Index(line, ": "); i >= 0 {
		head = line[:i]
	}
	toks := strings.Fields(head)
	if len(toks) < 2 || strings.ToUpper(toks[0]) != toks[0] || strings.Trim(toks[0], "ABCDEFGHIJKLMNOPQRSTUVWXYZ-") != "" {
		return ""
	}
	out := []string{toks[0]}
	for _, tok := range toks[1:] {
		if k, _, ok := strings.Cut(tok, "="); ok {
			out = append(out, k+"=")
			continue
		}
		out = append(out, tok)
	}
	return strings.Join(out, " ")
}

func secondWord(s string) string {
	f := strings.Fields(s)
	if len(f) < 2 {
		return ""
	}
	return f[1]
}
