package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// firstrun_test.go pins the onboarding standard for this binary: the usage
// banner's examples are RUN rather than read, and a refusal says what the input
// WANTS rather than only what was wrong. The central onboarding test in
// internal/ci builds the binary and checks the door; this file executes the
// lines behind it so an example that drifts out of the flag set is red here.

func runCIIn(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

// (a) Every line in the banner's example block runs, exits 0 or 1, and prints
// something on stdout. `slowtests` reads stdin, so an empty stream stands in
// for "no events yet" and must still be a green, not a hang.
func TestUsageBannerExamplesRun(t *testing.T) {
	exit, stdout, stderr := runCIIn(t, "", "help")
	if exit != 0 {
		t.Fatalf("`nova-ci help` exit = %d, want 0; stderr: %s", exit, stderr)
	}
	examples, err := onboarding.ExampleLines(stdout, "nova-ci")
	if err != nil {
		t.Fatalf("%v\n\n%s", err, stdout)
	}
	for _, ex := range examples {
		args := strings.Fields(ex)[1:]
		exit, out, errs := runCIIn(t, "", args...)
		if exit == 2 {
			t.Errorf("the usage example %q does not run: exit 2 (could not run)\nstderr: %s", ex, errs)
			continue
		}
		if exit != 0 {
			t.Errorf("the usage example %q ran but said NO (exit %d)\nstderr: %s", ex, exit, errs)
		}
		if out == "" {
			t.Errorf("the usage example %q printed nothing on stdout", ex)
		}
	}
}

// (b) The two refusals a first run hits name what the input wants: the whole
// seconds a budget must be, and the line of stdin that was not a TestEvent.
func TestARefusalSaysWhatTheInputWants(t *testing.T) {
	exit, _, stderr := runCIIn(t, "", "slowtests", "--budget", "0")
	if exit != 2 {
		t.Errorf("--budget 0 exit = %d, want 2", exit)
	}
	if !strings.Contains(stderr, "greater than zero") {
		t.Errorf("--budget 0 stderr = %q, want it to say the budget must be greater than zero", stderr)
	}

	exit, _, stderr = runCIIn(t, "not json\n", "slowtests")
	if exit != 2 {
		t.Errorf("a malformed stdin exit = %d, want 2", exit)
	}
	if !strings.Contains(stderr, "line 1") {
		t.Errorf("a malformed stdin stderr = %q, want it to name line 1", stderr)
	}
	if !strings.Contains(stderr, "run: nova-ci help") {
		t.Errorf("a refusal stderr = %q, want it to name the door", stderr)
	}
}

// (c) The `### First run` block of docs/TESTS.md is EXECUTED: every command in
// it is run, in order, and each one's whole output is compared with the block
// written under it -- same number of lines, same lines, same order. Before this
// test nothing in this package opened that document, so the two CI-SLOW lines a
// stranger copies were a promise no build checked.
//
// NOTHING IS NORMALISED HERE, and that is a property of this transcript rather
// than a shortcut: nova-ci reads a fixture on disk and prints what it counted,
// so every digit on both lines reproduces. onboarding.CompareTranscript is told
// so by being handed no field of the onboarding.Volatile table, and it says as
// much under any line that disagrees.
//
// The comparison is onboarding.CompareTranscript and nothing else, which is what
// TestEveryTranscriptIsExecutedLineForLine (internal/ci) asserts of every tool:
// RUNNING the steps is this package's business, because only this package knows
// how to call its own entry point, and COMPARING them is the one shared
// comparator's, so that a green here is worth what a green is worth everywhere.
//
// The transcript's paths (`cmd/nova-ci/testdata/example-events.jsonl`) are
// written from the root of the checkout, which is where a reader typing them
// stands, so the test moves there rather than rewriting them -- a rewritten
// path is no longer the line the document promised.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Chdir(repoRoot(t))
	raw, err := os.ReadFile(filepath.Join("docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-ci")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-ci", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatal("the `### First run` block holds no nova-ci command; this test would pass by running nothing")
	}
	// Both budgets are the point of the section: one over and one under, so a
	// reader sees the refusal and the green. A transcript that has lost one of
	// them still matches line for line and is still short of a first run.
	if len(steps) != 2 {
		t.Errorf("the `### First run` block runs %d commands, want 2: one budget the fixture exceeds and one it does not", len(steps))
	}
	// The sitting: every documented command, in order, in one temp-free run.
	// A command that could not be invoked at all stops the sitting, because
	// every line after it would be compared against a state that never happened.
	run := runDocumented(t)
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		res, err := run(s)
		if err != nil {
			t.Fatalf("the documented command\n  %s\ncould not be run: %v", s.Line, err)
		}
		got = append(got, res)
	}
	for _, p := range onboarding.CompareTranscript(steps, got, nil) {
		t.Error(p)
	}
}

// runDocumented calls this binary's own entry point with the documented
// arguments, opening the file a `< path` redirect names -- relative to the
// checkout root, where the test now stands and where the document's reader does.
func runDocumented(t *testing.T) onboarding.Runner {
	t.Helper()
	return func(s onboarding.Step) (onboarding.Result, error) {
		stdin := io.Reader(strings.NewReader(""))
		if s.Stdin != "" {
			f, err := os.Open(s.Stdin)
			if err != nil {
				return onboarding.Result{}, err
			}
			defer f.Close()
			stdin = f
		}
		var out, errb bytes.Buffer
		code := run(s.Args, stdin, &out, &errb)
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
	}
}

// repoRoot is the checkout root: this package sits two directories under it.
// It is resolved rather than assumed so that a failure names a path.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "TESTS.md")); err != nil {
		t.Fatalf("docs/TESTS.md is not under %s: %v", root, err)
	}
	return root
}
