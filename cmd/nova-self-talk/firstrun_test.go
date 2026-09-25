package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// The onboarding standard (ONBOARDING.md), pinned for this binary. A newcomer's
// first stumble is the spec for these tests: the usage banner's examples are RUN
// rather than read, every refusal a first run hits says what the input WANTS,
// and the README's transcript is compared against what the tool prints.

// examplePages is the fixture that ships with this tool: two small pages, one a
// journal and one a rule document, each carrying findings of a different class
// so that a first run sees what a finding looks like.
const examplePages = "testdata/example-pages"

func runSelfTalk(t *testing.T, args ...string) (exit int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	exit = run(args, &out, &errb)
	return exit, out.String(), errb.String()
}

// localize points an example or transcript command at the fixture pages, so
// what is under test is the command's SHAPE and not the reader's layout.
func localize(args []string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		if rest, ok := strings.CutPrefix(a, "./pages/"); ok {
			out[i] = filepath.Join(examplePages, rest)
		}
	}
	return out
}

func examples(t *testing.T) []string {
	t.Helper()
	// The banner is asked for, because a bare invocation no longer IS one: a refusal now
	// costs one line and the hint under it, and names the door (`run: nova-self-talk
	// help`). Reading it through that door is also a test that the door opens.
	exit, stdout, stderr := runSelfTalk(t, "help")
	if exit != 0 {
		t.Fatalf("`nova-self-talk help` must print the usage and exit 0, got %d; stderr: %s", exit, stderr)
	}
	lines, err := onboarding.ExampleLines(stdout, "nova-self-talk")
	if err != nil {
		t.Fatalf("%s\n\n%s", err, stdout)
	}
	return lines
}

// (a) The bare command prints usage ending in an `example:` block of lines that
// actually run. "Run" is this repo's exit law: 0 or 1 is an answer, 2 is "could
// not run". Both examples here answer 1, because both fixture pages carry
// findings — which is what a first run should be shown.
func TestUsageBannerExamplesRun(t *testing.T) {
	for _, ex := range examples(t) {
		exit, stdout, stderr := runSelfTalk(t, localize(strings.Fields(ex)[1:])...)
		if exit == 2 {
			t.Errorf("the usage example %q does not run: exit 2 (could not run)\nstderr: %s", ex, stderr)
			continue
		}
		if exit != 1 {
			t.Errorf("the usage example %q exits %d; the fixture pages carry findings, so an example that stopped flagging them has drifted", ex, exit)
		}
		if !strings.Contains(stdout, "SELFTALK NOTE") {
			t.Errorf("the usage example %q printed no NOTE; every completed run carries it\nstdout: %s", ex, stdout)
		}
	}
}

// (b) A refusal says what the flag or input WANTS, not only what was wrong.
func TestARefusalSaysWhatTheInputWants(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no files named", nil, filesHint},
		{"a path where a basename belongs", []string{"--skip", "memory/RULES.md", "x.md"}, "the match is on the file's name wherever it sits"},
		{"an empty basename", []string{"--rule-doc", "", "x.md"}, baseHint},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := runSelfTalk(t, tc.args...)
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

// (b), the other half: one run names every problem it can find. Three mistyped
// paths are three refusals, not the first one and a second trip.
func TestEveryUnreadableFileIsNamedInOneRun(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "real.md")
	if err := os.WriteFile(good, []byte("I cannot check my own work.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := []string{filepath.Join(dir, "one.md"), filepath.Join(dir, "two.md"), filepath.Join(dir, "three.md")}
	exit, stdout, stderr := runSelfTalk(t, append([]string{good}, missing...)...)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2; stderr: %s", exit, stderr)
	}
	for _, m := range missing {
		if !strings.Contains(stderr, m) {
			t.Errorf("one run must name every unreadable file; %q is missing from:\n%s", m, stderr)
		}
	}
	// And nothing was scanned: a run that printed findings and then refused
	// would be reporting findings from a run that did not happen.
	if strings.Contains(stderr, "SELFTALK FAIL") || strings.Contains(stdout, "SELFTALK") {
		t.Errorf("a refused run must scan nothing:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if !strings.Contains(stderr, "NOTHING was scanned") {
		t.Errorf("the refusal must say that nothing was scanned:\n%s", stderr)
	}
}

// (c) The `### First run` transcript of docs/TESTS.md is EXECUTED: every
// documented command is run and its whole output is compared with the block
// written under it -- same number of lines, same lines, same order.
//
// WHAT THIS REPLACES, AND WHY. The old test collected the SHAPES the command
// printed into a `printed map[string]bool` and asked whether each documented
// line was in it. A line the document DROPPED removed a lookup rather than an
// assertion, so the second block -- which passes two files and quoted only the
// first file's lines -- passed while showing TWO of the seven lines the tool
// prints. That is issue #1639, found by the 2026-09-19 dogfood rerun on hulk,
// reproduced on space and again here on the Studio, and it was invisible to the
// test that claimed to check it.
//
// TWO STREAMS, AND THAT IS THE OTHER HALF OF #1639. The findings go to standard
// error and the protocol lines to standard output, and the order a terminal
// interleaves them in is NOT the same twice: the last finding arrived after the
// NOTE line on one bench and before it on another (#1549). So the block is
// written to #1570's convention -- a line opening `! ` is standard error --
// and the two streams are compared apart: standard output whole, standard error
// for the lines shown, in order. Reading this block as one stream cannot be
// made to pass, and should not be.
//
// The documented paths are typed as written. `./pages` is a copy of the fixture
// in a directory of the test's own, because the tool PRINTS THE PATH BACK on
// every finding: a rewritten path is no longer the line the document promised,
// which is what the old test's `localize` gave up.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	doc := transcriptDoc(t)
	lines, err := onboarding.FirstRun(doc, "nova-self-talk")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-self-talk", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("the `### First run` block runs %d commands, want 2: one file, then the rule document beside it", len(steps))
	}

	dir := t.TempDir()
	copyDir(t, examplePages, filepath.Join(dir, "pages"))
	t.Chdir(dir)

	for _, p := range onboarding.Execute(steps, runDocumented) {
		t.Error(p)
	}
}

// runDocumented calls this binary's own entry point with the documented
// arguments, keeping the two streams apart.
func runDocumented(s onboarding.Step) (onboarding.Result, error) {
	if s.Stdin != "" {
		return onboarding.Result{}, errReadsNothing
	}
	var out, errb bytes.Buffer
	code := run(s.Args, &out, &errb)
	return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
}

type readsNothing struct{}

func (readsNothing) Error() string {
	return "nova-self-talk reads no stdin; a `< path` in its transcript is the document's bug"
}

var errReadsNothing = readsNothing{}

// transcriptDoc is docs/TESTS.md, read BEFORE the test moves into its own
// directory.
func transcriptDoc(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// copyDir copies the fixture pages to where the transcript says they are.
func copyDir(t *testing.T, from, to string) {
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
			copyDir(t, filepath.Join(from, e.Name()), filepath.Join(to, e.Name()))
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
