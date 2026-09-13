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

// (c) The README's `### First run` transcript, checked against the tool: every
// transcript line must match a line the tool actually printed, by event prefix
// and field names in order. The sentences quoted in it are the fixture's own
// and are not compared — the tail after ": " is a run's business.
func TestREADMEFirstRunMatchesWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-self-talk")
	if err != nil {
		t.Fatal(err)
	}
	var printed map[string]bool
	seen := map[string]int{}
	for _, line := range lines {
		if cmd, ok := strings.CutPrefix(line, "$ nova-self-talk "); ok {
			exit, stdout, stderr := runSelfTalk(t, localize(strings.Fields(cmd))...)
			if exit == 2 {
				t.Fatalf("the README command %q does not run: exit 2, stderr: %s", line, stderr)
			}
			printed = map[string]bool{}
			for _, out := range strings.Split(stdout+"\n"+stderr, "\n") {
				if s := onboarding.Shape(out); s != "" {
					printed[s] = true
				}
			}
			continue
		}
		s := onboarding.Shape(line)
		if s == "" {
			continue
		}
		if printed == nil {
			t.Fatalf("transcript line before any command: %q", line)
		}
		if !printed[s] {
			t.Errorf("README line\n  %s\nhas shape %q, which this tool never prints. Re-run the command and paste what it said.", line, s)
		}
		seen[strings.Join(strings.Fields(s)[:2], " ")]++
	}
	for prefix, want := range map[string]int{
		// Four FAIL lines: two findings and the count line in the first transcript, one
		// finding in the second. The count line is one of them on purpose -- it is the
		// line that was missing on a failing run, and a README that did not show it
		// would be teaching the shape this change exists to fix.
		"SELFTALK FAIL": 4, "SELFTALK DATED": 1, "SELFTALK NOTE": 1, "SELFTALK RULEDOC": 1,
	} {
		if seen[prefix] != want {
			t.Errorf("README First run shows %d %s lines, want %d", seen[prefix], prefix, want)
		}
	}
}
