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
// rather than read, every refusal a first run hits must say what the flag WANTS,
// and the README's transcript is compared against what the tool actually prints.
// Guidance nothing checks rots into a claim about a message that has since moved.

// exampleSelf is the fixture self repo that ships with this tool: a five-file
// tree the size of a first run, referenced by nothing outside testdata.
const exampleSelf = "testdata/example-self"

func runCheck(t *testing.T, args ...string) (exit int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	exit = run(args, &out, &errb)
	return exit, out.String(), errb.String()
}

// localize points an example or transcript command at the fixture, so what is
// under test is the command's SHAPE and not the reader's directory layout.
func localize(args []string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		if a == "./self" {
			out[i] = exampleSelf
		} else if rest, ok := strings.CutPrefix(a, "./self/"); ok {
			out[i] = filepath.Join(exampleSelf, rest)
		}
	}
	return out
}

func examples(t *testing.T) []string {
	t.Helper()
	// The banner is asked for, because a bare invocation no longer IS one: a refusal now
	// costs one line and names the door (`run: nova-check help`). Reading it through that
	// door is also a test that the door opens.
	exit, stdout, stderr := runCheck(t, "help")
	if exit != 0 {
		t.Fatalf("`nova-check help` must print the usage and exit 0, got %d; stderr: %s", exit, stderr)
	}
	lines, err := onboarding.ExampleLines(stdout, "nova-check")
	if err != nil {
		t.Fatalf("%s\n\n%s", err, stdout)
	}
	return lines
}

// (a) The bare command prints usage ending in an `example:` block of lines that
// actually run. They are run here against the fixture: an example that has
// drifted out of the flag set teaches the wrong invocation to exactly the
// reader who cannot tell.
func TestUsageBannerExamplesRun(t *testing.T) {
	for _, ex := range examples(t) {
		exit, stdout, stderr := runCheck(t, localize(strings.Fields(ex)[1:])...)
		if exit == 2 {
			t.Errorf("the usage example %q does not run: exit 2 (could not run)\nstderr: %s", ex, stderr)
			continue
		}
		if exit != 0 {
			t.Errorf("the usage example %q ran but said NO (exit %d); an example a stranger types should pass on the fixture\nstderr: %s", ex, exit, stderr)
		}
		if stdout == "" {
			t.Errorf("the usage example %q printed nothing on stdout", ex)
		}
	}
}

// Every verb the banner names is one a reader may type first, so every one of
// them appears in the usage block above the examples.
func TestQuickstartIsTheFirstThingTheBannerOffers(t *testing.T) {
	exs := examples(t)
	if !strings.HasPrefix(exs[0], "nova-check quickstart ") {
		t.Errorf("the first example is %q; a first run should be offered quickstart first", exs[0])
	}
	if !strings.Contains(usage, "nova-check quickstart --dir <dir>") {
		t.Error("the usage block does not list the quickstart verb")
	}
}

// (b) A refusal says what the flag or input WANTS, not only what was wrong.
func TestARefusalSaysWhatTheFlagWants(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"quickstart without a directory", []string{"quickstart"}, dirHint},
		{"nocode without a directory", []string{"nocode"}, dirHint},
		{"links without a directory", []string{"links"}, dirHint},
		{"attest without a home", []string{"attest", "--manifest", "MANIFEST"}, homeHint},
		{"attest without a manifest", []string{"attest", "--home", exampleSelf}, manifestHint},
		{"kernel without a file", []string{"kernel", "--max-bytes", "4000"}, fileHint},
		{"kernel without a budget", []string{"kernel", "--file", "k.md"}, budgetHint},
		{"kernel with both budgets", []string{"kernel", "--file", "k.md", "--max-bytes", "1", "--max-tokens", "1"}, budgetHint},
		{"kernel with a token budget and no divisor", []string{"kernel", "--file", "k.md", "--max-tokens", "400"}, budgetHint},
		{"floors without a door", []string{"floors", "--source", "SEED.md"}, coreHint},
		{"floors without a source", []string{"floors", "--core", "SEED-CORE.md"}, sourceHint},
		{"corpus without a ledger", []string{"corpus", "--root", ".", "--min-anchors", "1"}, ledgerHint},
		{"corpus without a root", []string{"corpus", "--ledger", "l.md", "--min-anchors", "1"}, rootHint},
		{"corpus without a row floor", []string{"corpus", "--ledger", "l.md", "--root", "."}, anchorsHint},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := runCheck(t, tc.args...)
			if exit != 2 {
				t.Fatalf("exit = %d, want 2 — guidance must not soften the refusal; stderr: %s", exit, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q,\nwant it to contain the hint %q", stderr, tc.want)
			}
			if stdout != "" {
				t.Errorf("a refusal must print nothing on stdout, got %q", stdout)
			}
		})
	}
}

// (b), the other half: a run reports every problem it can find in one go. These
// flags are independent of each other, so a caller who omitted two learns about
// two — being sent back a second time for something the first run could already
// see is the stumble this pins shut.
func TestIndependentProblemsAreReportedInOneRun(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"kernel with nothing at all", []string{"kernel"}, []string{"--file is required", "--max-bytes or --max-tokens is required"}},
		{"corpus with nothing at all", []string{"corpus"}, []string{"--ledger is required", "--root is required", "--min-anchors is required"}},
		{"attest with nothing at all", []string{"attest"}, []string{"--home is required", "--manifest is required"}},
		{"floors with nothing at all", []string{"floors"}, []string{"--core is required", "--source is required"}},
		{"a token budget of zero with no divisor", []string{"kernel", "--file", "k.md", "--max-tokens", "0"}, []string{"--max-tokens requires --bytes-per-token", "--max-tokens must be a positive"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, _, stderr := runCheck(t, tc.args...)
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

// quickstart runs BOTH checks even when the first says NO, for the same reason:
// a first run should learn everything this pair can tell it in one go.
func TestQuickstartRunsBothChecksAndTakesTheWorstExit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("[gone](nowhere.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build.py"), []byte("print('machinery')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr := runCheck(t, "quickstart", "--dir", dir)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1 (both checks ran and said NO); stderr: %s", exit, stderr)
	}
	if !strings.Contains(stderr, "LINKS FAIL") {
		t.Errorf("the broken link is not reported:\n%s", stderr)
	}
	if !strings.Contains(stderr, "NOCODE FAIL") {
		t.Errorf("nocode did not run after links failed; a first run must get both:\n%s", stderr)
	}
	if !strings.Contains(stdout, "QUICKSTART OK done=2 worst-exit=1") {
		t.Errorf("the closing line must report both checks and the worst exit:\n%s", stdout)
	}
}

// (c) The README's `### First run` transcript, checked against the tool. The
// commands in it are run here against the fixture, and every transcript line
// must match a line the tool actually printed — the event prefix and the field
// names, in order. Numbers, paths and tails are a run's own business and are
// deliberately NOT compared: pinning those would make the README a fixture
// instead of a document.
func TestREADMEFirstRunMatchesWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-check")
	if err != nil {
		t.Fatal(err)
	}
	var printed map[string]bool
	seen := map[string]int{}
	for _, line := range lines {
		if cmd, ok := strings.CutPrefix(line, "$ nova-check "); ok {
			exit, stdout, stderr := runCheck(t, localize(strings.Fields(cmd))...)
			if exit == 2 {
				t.Fatalf("the README command %q does not run: exit 2, stderr: %s", line, stderr)
			}
			printed = map[string]bool{}
			for _, out := range strings.Split(stdout, "\n") {
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
		"QUICKSTART OK": 2, "LINKS OK": 1, "NOCODE OK": 1, "KERNEL OK": 1,
	} {
		if seen[prefix] != want {
			t.Errorf("README First run shows %d %s lines, want %d", seen[prefix], prefix, want)
		}
	}
}
