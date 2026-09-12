package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// The onboarding standard (ONBOARDING.md), pinned for this binary: the usage banner's
// examples are RUN rather than read, every refusal a first run hits says what the flag
// WANTS and one run names every independent problem, and the README transcript is
// compared against what the tool actually prints.

// firstRunLab is a lab whose fixture holds one entry a first run can look at, so that the
// example sitting has something to say.
func firstRunLab(t *testing.T) *lab {
	l := newLab(t)
	oid := l.branch("rowan/twin-full-width-lanes", "lanes.md", "the change\n", "a change")
	l.host.PRs[949] = merge.PR{Number: 949, Author: "pat", Base: "main",
		HeadRef: "rowan/twin-full-width-lanes", HeadOID: oid, Mergeable: "MERGEABLE",
		URL: "https://example.invalid/949"}
	l.host.SetChecks(oid, 4, 1)
	l.host.SetChecks(l.baseSHA(), 4, 0)
	return l
}

// localize points an example or transcript command at this test's lane. The example says
// ./lane because a stranger types a path of their own; the test gives it one under
// t.TempDir(), and the repository resolves to the bare fixture repo.
func (l *lab) localize(args []string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		if a == "./lane" {
			out[i] = l.lane
		}
	}
	return out
}

func exampleLines(t *testing.T, l *lab) []string {
	t.Helper()
	exit, stdout, stderr := l.run("help")
	if exit != 0 {
		t.Fatalf("`nova-merge help` must print the usage and exit 0, got %d; stderr: %s", exit, stderr)
	}
	lines, err := onboarding.ExampleLines(stdout, "nova-merge")
	if err != nil {
		t.Fatalf("%s\n\n%s", err, stdout)
	}
	return lines
}

// (a) The usage banner ends in an `example:` block of lines that ACTUALLY RUN. "Run" is
// this repo's exit law: 0 or 1 is an answer and 2 is "could not run".
func TestUsageBannerExamplesRun(t *testing.T) {
	t.Parallel()
	l := firstRunLab(t)
	exs := exampleLines(t, l)
	if len(exs) != 5 {
		t.Fatalf("want the five-line sitting under `example:`, got %d: %q", len(exs), exs)
	}
	for _, ex := range exs {
		args := l.localize(strings.Fields(ex)[1:])
		exit, stdout, stderr := l.run(args...)
		if exit == 2 {
			t.Errorf("the usage example %q does not run: exit 2 (could not run)\nstderr: %s", ex, stderr)
			continue
		}
		if stdout == "" && stderr == "" {
			t.Errorf("the usage example %q printed nothing", ex)
		}
	}
}

// (b) A refusal says what the flag or input WANTS, not only what was wrong.
func TestARefusalSaysWhatTheFlagWants(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"init with no lane", []string{"init", "--repo", "o/n", "--base", "main", "--lane-branch", "l"}, "the lane's own directory"},
		{"init with no repo", []string{"init", "--lane", l.lane, "--base", "main", "--lane-branch", "l"}, "as <owner>/<name>"},
		{"read with no head", []string{"read", "--lane", l.lane, "--pr", "9", "--who", "emma", "--verdict", "approve"}, "the full 40-character sha the reader had open"},
		{"read with a short head", []string{"read", "--lane", l.lane, "--pr", "9", "--who", "emma", "--head", "cbde1fc6ba10", "--verdict", "approve"}, "a truncated sha might name the wrong commit"},
		{"gate with no base-sha", []string{"gate", "--lane", l.lane, "--pr", "9", "--head", strings.Repeat("a", 40), "--merge", strings.Repeat("c", 40), "--verdict", "green", "--summary", "x"}, "--base-sha is required"},
		{"gate with no merge", []string{"gate", "--lane", l.lane, "--pr", "9", "--head", strings.Repeat("a", 40), "--base-sha", strings.Repeat("b", 40), "--verdict", "green", "--summary", "x"}, "--merge is required"},
		{"run with neither once nor loop", []string{"run", "--lane", l.lane}, "--once or --loop <duration> is required"},
		{"loop with no hours", []string{"run", "--lane", l.lane, "--loop", "5m"}, "--loop requires --hours"},
		{"packet with no who", []string{"packet", "--lane", l.lane, "--all"}, "the reader this packet is for"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := l.run(tc.args...)
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

// (b), the other half: ONE RUN NAMES EVERY PROBLEM IT CAN FIND. A caller can fix two
// things as easily as one, and sending a first run back three times for three independent
// flags is three refusals the first one already knew about.
func TestIndependentProblemsAreReportedInOneRun(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"init with nothing at all", []string{"init"}, []string{"--lane is required", "--repo is required", "--base is required", "--lane-branch is required"}},
		{"gate with a bad sha and no summary", []string{"gate", "--lane", l.lane, "--pr", "9", "--head", "abc", "--base-sha", "def", "--merge", "ghi", "--verdict", "green"},
			[]string{"--head wants a full 40-character sha", "--base-sha wants a full 40-character sha", "--merge wants a full 40-character sha", "--summary is required"}},
		{"read with nothing", []string{"read", "--lane", l.lane}, []string{"--pr <n> or --branch <name> is required", "--who is required", "--head is required", "--verdict is approve or hold"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, _, stderr := l.run(tc.args...)
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

// (c) The README's `### First run` transcript, checked against the tool: every transcript
// line must match a line the tool actually printed, by event prefix and field names in
// order. Shas, paths and counts are a run's own business and are deliberately not
// compared, so the transcript stays a document rather than becoming a fixture.
func TestREADMEFirstRunMatchesWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-merge")
	if err != nil {
		t.Fatal(err)
	}
	l := firstRunLab(t)
	var printed map[string]bool
	seen := map[string]int{}
	for _, line := range lines {
		if cmd, ok := strings.CutPrefix(line, "$ "); ok {
			exit, stdout, stderr := l.run(l.localize(strings.Fields(cmd)[1:])...)
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
	for prefix, want := range map[string]int{"INIT OK": 1, "STATUS OK": 2, "ADD OK": 1, "STATUS ENTRY": 1} {
		if seen[prefix] != want {
			t.Errorf("README First run shows %d %s lines, want %d", seen[prefix], prefix, want)
		}
	}
}
