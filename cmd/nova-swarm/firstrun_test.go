package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// The onboarding standard (ONBOARDING.md), pinned for this binary: the usage banner's
// examples are RUN rather than read, the refusals a first run hits say what the flag WANTS
// and name every independent problem in one go, and the README transcript's shape is
// compared against what the tool actually prints. Guidance nothing checks rots into a claim
// about a message that has since moved.

func runSwarm(t *testing.T, args ...string) (exit int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	exit = run(args, strings.NewReader(""), &out, &errb, time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))
	return exit, out.String(), errb.String()
}

// localize points an example or a transcript command at a pool this test makes, so what is
// under test is the command's SHAPE and not the reader's directory layout. Nothing here
// reaches outside t.TempDir().
func localize(t *testing.T, pool string, args []string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		if a == "./pool" {
			out[i] = pool
		}
	}
	return out
}

func examples(t *testing.T) []string {
	t.Helper()
	exit, stdout, stderr := runSwarm(t, "help")
	require.Equal(t, 0, exit, "`nova-swarm help` must print the usage and exit 0, got %d; stderr: %s", exit, stderr)
	lines, err := onboarding.ExampleLines(stdout, "nova-swarm")
	require.NoError(t, err, "\n%s", stdout)
	return lines
}

// (a) The usage banner ends in an `example:` block of lines that actually run. They are run
// here: an example that has drifted out of the flag set teaches the wrong invocation to
// exactly the reader who cannot tell.
func TestUsageBannerExamplesRun(t *testing.T) {
	t.Parallel()

	pool := filepath.Join(t.TempDir(), "pool")
	for _, ex := range examples(t) {
		exit, stdout, stderr := runSwarm(t, localize(t, pool, strings.Fields(ex)[1:])...)
		if !assert.NotEqual(t, 2, exit, "the usage example %q does not run: exit 2 (could not run)\nstderr: %s", ex, stderr) {
			continue
		}
		assert.Equal(t, 0, exit, "the usage example %q ran but said NO (exit %d)\nstderr: %s", ex, exit, stderr)
		assert.NotEmpty(t, stdout, "the usage example %q printed nothing on stdout", ex)
	}
}

// (a2) The `example:` block is pasted top to bottom by a stranger in an empty directory, so
// the first line that names ./pool must be the one that makes it (quickstart); a line that
// reads ./pool before then exits 2 as pasted, whatever localize does for the test above.
// No prose setup line outside the block: onboarding.ExampleLines never runs it (#1455).
func TestUsageBannerExamplesMakeThePoolBeforeReadingIt(t *testing.T) {
	t.Parallel()

	for _, ex := range examples(t) {
		f := strings.Fields(ex)
		for _, a := range f {
			if a != "./pool" {
				continue
			}
			require.GreaterOrEqual(t, len(f), 2, "the usage example %q reads ./pool before any example makes it; put `nova-swarm quickstart --pool ./pool` above it", ex)
			require.Equal(t, "quickstart", f[1], "the usage example %q reads ./pool before any example makes it; put `nova-swarm quickstart --pool ./pool` above it", ex)
			return
		}
	}
}

// (b) A bare invocation costs ONE line and names the door, rather than 60 lines of banner
// on every flag typo.
func TestABareInvocationCostsOneLineAndNamesTheDoor(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runSwarm(t)
	assert.Equal(t, 2, exit, "a bare nova-swarm exits %d, want 2", exit)
	assert.Empty(t, stdout, "a refusal belongs on stderr, got stdout: %q", stdout)
	lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	assert.Len(t, lines, 1, "a bare nova-swarm printed %d lines, want 1:\n%s", len(lines), stderr)
	assert.Contains(t, stderr, "run: nova-swarm help", "a bare nova-swarm names no door:\n%s", stderr)
}

// (c) The README transcript is compared against what the tool prints -- the event prefixes
// and the field names, never the values, so the transcript stays a document rather than
// becoming a fixture.
func TestTheReadmeTranscriptIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-swarm")
	require.NoError(t, err)
	pool := filepath.Join(t.TempDir(), "pool")
	var want []string
	var printed []string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "$ "):
			args := localize(t, pool, strings.Fields(strings.TrimPrefix(line, "$ "))[1:])
			exit, stdout, stderr := runSwarm(t, args...)
			require.Equal(t, 0, exit, "the transcript's `%s` exited %d: %s", line, exit, stderr)
			for _, out := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
				if shape := onboarding.Shape(out); shape != "" {
					printed = append(printed, shape)
				}
			}
		default:
			if shape := onboarding.Shape(line); shape != "" {
				want = append(want, shape)
			}
		}
	}
	require.NotEmpty(t, want, "the transcript holds no event line")
	assert.Equal(t, strings.Join(want, "\n"), strings.Join(printed, "\n"), "the README transcript and the tool disagree.\ntranscript:\n%s\n\nprinted:\n%s",
		strings.Join(want, "\n"), strings.Join(printed, "\n"))
}

// (d) The `### First run` block of docs/TESTS.md is EXECUTED: every command in
// it is run, in order, and each one's whole output is compared with the block
// written under it -- same number of lines, same lines, same order. (c) above
// compares only SHAPE, which lets an abridged or reordered transcript pass; this
// test keeps the whole promise for nova-swarm the way cmd/nova-ci already does.
//
// NOTHING IS NORMALISED HERE, and that is a property of this transcript rather
// than a shortcut: both documented commands read or make a pool whose path the
// tool echoes back exactly as `./pool`, and neither prints a stamp, an id or a
// duration. Every value on both lines reproduces, so onboarding.Execute is
// handed no Norm and says as much under any line that disagrees.
//
// The transcript's `--pool ./pool` is written from wherever the reader's shell
// stands, and quickstart MAKES that directory, so the test does not rewrite the
// documented path: it moves to a temp directory where `./pool` is the test's to
// create, and the tool echoes back the relative `./pool` it was handed.
func TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-swarm")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-swarm", lines)
	require.NoError(t, err)
	require.NotEmpty(t, steps, "the `### First run` block holds no nova-swarm command; this test would pass by running nothing")
	// Both commands are the point of the section: quickstart makes the pool and
	// names the next moves, status reports it empty. A transcript that has lost
	// one of them still matches line for line and is still short of a first run.
	assert.Len(t, steps, 1, "the `### First run` block runs %d commands, want 1: template", len(steps))
	t.Chdir(t.TempDir())
	for _, p := range onboarding.Execute(steps, runDocumentedSwarm(t)) {
		t.Error(p)
	}
}

// runDocumentedSwarm calls this binary's own entry point with the documented
// arguments, opening the file a `< path` redirect names. The transcript's paths
// are relative to the directory the test now stands in, which is where the
// document's reader types them.
func runDocumentedSwarm(t *testing.T) onboarding.Runner {
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
		code := run(s.Args, stdin, &out, &errb, time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
	}
}

// TestTheCommandReferenceFirstRunIsWhatTheToolPrints executes docs/CLI.md's
// `### First run` block the same way: the reference is a document a stranger
// pastes from, and its one command (template) is run.
func TestTheCommandReferenceFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "CLI.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-swarm")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-swarm", lines)
	require.NoError(t, err)
	require.Len(t, steps, 1, "the `### First run` block of docs/CLI.md runs %d commands, want 1: template", len(steps))
	pool := filepath.Join(t.TempDir(), "pool")
	for i := range steps {
		steps[i].Args = localize(t, pool, steps[i].Args)
	}
	for _, p := range onboarding.Execute(steps, runDocumentedSwarm(t), onboarding.Path("./pool", pool)) {
		t.Error(p)
	}
}

// TestUsageBannerExamplesRunThroughTheComparator: the banner's `example:` block
// is pasted top to bottom by a stranger, so each line is run, in order, through
// the one comparator. template --name read-pr prints a
// document, and is compared against the template package swarm holds.
func TestUsageBannerExamplesRunThroughTheComparator(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "CLI.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-swarm")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-swarm", lines)
	require.NoError(t, err)
	documented := make(map[string]onboarding.Step, len(steps))
	for _, s := range steps {
		documented[strings.TrimPrefix(s.Line, "$ ")] = s
	}
	// The block, held by name: an example that quietly left the banner would
	// otherwise leave this test running less than the stranger pastes.
	linesOfTheBanner := []string{
		"nova-swarm template --name read-pr",
		"nova-swarm template --name worker",
		"nova-swarm lint --rules",
	}
	got := examples(t)
	require.Equal(t, strings.Join(linesOfTheBanner, "\n"), strings.Join(got, "\n"), "the banner's example block is not the sitting this test runs\nbanner:\n  %s\nwant:\n  %s",
		strings.Join(got, "\n  "), strings.Join(linesOfTheBanner, "\n  "))
	pool := filepath.Join(t.TempDir(), "pool")
	norms := []onboarding.Norm{onboarding.Path("./pool", pool)}
	run := runDocumentedSwarm(t)
	for _, ex := range got {
		step, ok := documented[ex]
		if !ok {
			// A line the command reference does not show prints a template or
			// the lint's rule set: what it must print is that template's body,
			// or one LINT RULE line per rule with its remedy, in order.
			var want []string
			switch name, isTemplate := strings.CutPrefix(ex, "nova-swarm template --name "); {
			case isTemplate:
				body, err := swarm.Template(name)
				require.NoError(t, err)
				want = strings.Split(strings.TrimSuffix(body, "\n"), "\n")
			case ex == "nova-swarm lint --rules":
				for _, rule := range cardLintRuleNames() {
					want = append(want, "LINT RULE "+oneline.Field(rule)+" remedy="+oneline.Escape(cardLintRemedies[rule]))
				}
			default:
				t.Fatalf("the banner example %q is in no `### First run` block of docs/CLI.md and prints neither a template nor the rule set", ex)
			}
			step = onboarding.Step{Line: "$ " + ex, Args: strings.Fields(ex)[1:], Want: want}
		}
		step.Args = localize(t, pool, step.Args)
		res, err := run(step)
		require.NoError(t, err, "the banner example\n  %s\ncould not be run", ex)
		for _, p := range onboarding.Compare(step, res, norms) {
			t.Error(p)
		}
	}
}
