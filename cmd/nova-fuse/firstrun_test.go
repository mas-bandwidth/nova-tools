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

// (c) The README's `### First run` transcript, checked against the tool: every
// transcript line must match a line the tool actually printed, by event prefix
// and field names in order. Timestamps, surface names and reasons are a run's
// own business and are deliberately not compared.
func TestREADMEFirstRunMatchesWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-fuse")
	if err != nil {
		t.Fatal(err)
	}
	box := freshBox(t)
	var printed map[string]bool
	seen := map[string]int{}
	for _, line := range lines {
		if cmd, ok := strings.CutPrefix(line, "$ "); ok {
			exit, stdout, stderr := runFuse(t, localize(fields(cmd), box)...)
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
		"STATUS OK": 2, "FUSE FAIL": 2, "QUARANTINE OK": 1, "LIFT OK": 2,
	} {
		if seen[prefix] != want {
			t.Errorf("README First run shows %d %s lines, want %d", seen[prefix], prefix, want)
		}
	}
}
