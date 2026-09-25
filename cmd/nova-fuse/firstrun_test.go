package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// The onboarding standard (ONBOARDING.md), pinned for this binary. A newcomer's
// first stumble is the spec for these tests: the usage banner's examples are RUN
// rather than read, every refusal a first run hits says what the flag or input
// WANTS, and the README's transcript is compared against what the tool prints.

// exampleBox is the fixture box that ships with this tool: one surface already
// quarantined, so that a first `status` has something to say. Every test copies
// it into t.TempDir() first, because these verbs write.
const exampleBox = "testdata/example-box.json"

func freshBox(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(exampleBox)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fuse-box.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// runFuse runs one invocation and returns both streams, because a transcript is
// what a terminal shows: this tool's gate answers on stderr and its writes
// answer on stdout, and the reader sees them interleaved.
func runFuse(t *testing.T, args ...string) (exit int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	exit = run(args, &out, &errb, time.Date(2026, 9, 9, 18, 27, 40, 0, time.UTC))
	return exit, out.String(), errb.String()
}

// localize points an example or transcript command at a box under t.TempDir().
func localize(args []string, box string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		if a == "./fuse-box.json" {
			out[i] = box
		}
	}
	return out
}

func examples(t *testing.T) []string {
	t.Helper()
	// The banner is asked for, because a bare invocation no longer IS one: a refusal now
	// costs one line and names the door (`run: nova-fuse help`). Reading it through that
	// door is also a test that the door opens.
	exit, stdout, stderr := runFuse(t, "help")
	if exit != 0 {
		t.Fatalf("`nova-fuse help` must print the usage and exit 0, got %d; stderr: %s", exit, stderr)
	}
	lines, err := onboarding.ExampleLines(stdout, "nova-fuse")
	if err != nil {
		t.Fatalf("%s\n\n%s", err, stdout)
	}
	return lines
}

// (a) The bare command prints usage ending in an `example:` block of lines that
// actually run. "Run" is this repo's own exit law: 0 or 1 is an answer, and 2 is
// "could not run". The five examples are one sitting and are executed in order
// against one box, because that is how a reader will type them.
func TestUsageBannerExamplesRun(t *testing.T) {
	box := freshBox(t)
	exs := examples(t)
	if len(exs) != 5 {
		t.Fatalf("want the five-line sitting under `example:`, got %d: %q", len(exs), exs)
	}
	for _, ex := range exs {
		exit, stdout, stderr := runFuse(t, localize(fields(ex), box)...)
		if exit == 2 {
			t.Errorf("the usage example %q does not run: exit 2 (could not run)\nstderr: %s", ex, stderr)
			continue
		}
		if stdout == "" && stderr == "" {
			t.Errorf("the usage example %q printed nothing", ex)
		}
	}
	// The sitting has to end where it started, or it is not a sitting: the
	// surface it quarantined is lifted again by its last line.
	exit, stdout, _ := runFuse(t, "check", "--box", box, "a-forum")
	if exit != 0 {
		t.Errorf("after the example sitting, a-forum is still not clear (exit %d): %s", exit, stdout)
	}
}

// fields splits an example command line the way the reader's shell does — a
// double-quoted run is one argument, so a reason stays one reason — and drops
// the tool's own name. A test that split on spaces alone would be exercising a
// command nobody types.
func fields(example string) []string {
	var (
		out     []string
		cur     strings.Builder
		quoted  bool
		started bool
	)
	flush := func() {
		if started {
			out = append(out, cur.String())
			cur.Reset()
			started = false
		}
	}
	for _, r := range example {
		switch {
		case r == '"':
			quoted = !quoted
			started = true
		case r == ' ' && !quoted:
			flush()
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	flush()
	if len(out) == 0 {
		return nil
	}
	return out[1:]
}

// (b) A refusal says what the flag or input WANTS, not only what was wrong.
func TestARefusalSaysWhatTheFlagWants(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"status with no box", []string{"status"}, boxHint},
		{"check with no box", []string{"check"}, boxHint},
		{"quarantine with no box", []string{"quarantine", "a-forum", "a reason"}, boxHint},
		{"lockdown with no box", []string{"lockdown", "a reason"}, boxHint},
		{"lift quarantine with no box", []string{"lift", "quarantine", "a-forum"}, boxHint},
		{"path with no box", []string{"path"}, boxHint},
		{"quarantine with no reason", []string{"quarantine", "--box", "b.json", "a-forum"}, "needs a surface and a reason"},
		{"lockdown with no reason", []string{"lockdown", "--box", "b.json"}, "needs a reason"},
		{"lift with no power", []string{"lift"}, "takes a power first"},
		{"lift lockdown", []string{"lift", "lockdown"}, "go talk with your person now"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := runFuse(t, tc.args...)
			if exit != 2 {
				t.Fatalf("exit = %d, want 2; stderr: %s", exit, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q,\nwant it to contain %q", stderr, tc.want)
			}
			if stdout != "" {
				t.Errorf("a refusal must print nothing on stdout, got %q", stdout)
			}
		})
	}
}

// (b), the other half: one run names every problem it can find. The box and the
// verb's own arguments are independent, so a caller who gave neither should not
// be sent back twice.
func TestIndependentProblemsAreReportedInOneRun(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"quarantine with nothing at all", []string{"quarantine"}, []string{"--box is required", "needs a surface and a reason"}},
		{"lockdown with nothing at all", []string{"lockdown"}, []string{"--box is required", "needs a reason"}},
		{"lift quarantine with nothing at all", []string{"lift", "quarantine"}, []string{"--box is required", "needs exactly one surface"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, _, stderr := runFuse(t, tc.args...)
			if exit != 2 {
				t.Fatalf("exit = %d, want 2; stderr: %s", exit, stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr, want) {
					t.Errorf("one run must name every problem it can find; %q is missing from:\n%s", want, stderr)
				}
			}
		})
	}
}

// The `### First run` block of docs/TESTS.md is EXECUTED: every documented
// command is run, in order, against one box, and its whole output is compared
// with the block written under it -- same number of lines, same lines, same
// order.
//
// WHAT THIS REPLACES. The old test collected the SHAPES a command printed into
// a `printed map[string]bool`, with "timestamps, surface names and reasons" --
// which is to say the whole of what a quarantine IS -- deliberately not
// compared. Under it `FUSE FAIL quarantine=a-forum since=...: a post addressed
// me and asked for a token` and `FUSE FAIL quarantine=anything since=...:
// whatever` are the same line, and a dropped line removes a lookup rather than
// an assertion.
//
// NOTHING IS NORMALISED, INCLUDING THE INSTANTS. This tool takes its clock as
// an argument, and the test hands it the instant the document was recorded at
// (see runFuse), so `since=2026-09-09T18:27:40Z` reproduces exactly and is
// compared as written. The fixture's older `since=2026-09-08T21:14:00Z` is a
// value in the box and reproduces for a different reason -- it was never this
// run's to invent. Both are compared, which a `since=` normalisation would have
// given up on for the sake of the one.
//
// The box is typed as written: `./fuse-box.json` is what a reader types, so the
// fixture is copied to that name in a directory of the test's own rather than
// the path being rewritten.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(exampleBox)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-fuse")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-fuse", lines)
	if err != nil {
		t.Fatal(err)
	}
	// The sitting is the whole gate: what is quarantined, the refusal that
	// enforces it, a new quarantine, its refusal, and the lift that ends it. A
	// block that has quietly lost one of the verbs is short of a first run in a
	// way no per-line comparison would say, because the lines that remain match.
	verbs := map[string]bool{}
	for _, s := range steps {
		verbs[s.Args[0]] = true
	}
	for _, verb := range []string{"status", "check", "quarantine", "lift"} {
		if !verbs[verb] {
			t.Errorf("the `### First run` block never runs `nova-fuse %s`; the first sitting is all four verbs", verb)
		}
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fuse-box.json"), fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	for _, p := range onboarding.Execute(steps, documented(t)) {
		t.Error(p)
	}
}

// documented runs one line of the transcript with the clock the document was
// recorded at.
func documented(t *testing.T) onboarding.Runner {
	t.Helper()
	return func(s onboarding.Step) (onboarding.Result, error) {
		if s.Stdin != "" {
			return onboarding.Result{}, errReadsNothing
		}
		code, stdout, stderr := runFuse(t, s.Args...)
		return onboarding.Result{Code: code, Stdout: stdout, Stderr: stderr}, nil
	}
}

type readsNothing struct{}

func (readsNothing) Error() string {
	return "nova-fuse reads no stdin; a `< path` in its transcript is the document's bug"
}

var errReadsNothing = readsNothing{}
