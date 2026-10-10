package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	path := filepath.Join(t.TempDir(), "fuse-box.json")
	testkit.WriteFile(t, path, testkit.ReadFile(t, exampleBox))
	return path
}

var fuseFixed = testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr, time.Date(2026, 9, 9, 18, 27, 40, 0, time.UTC), invocation{getenv: getenvNone, wd: "", stamp: version})
})

// runFuse runs one invocation and returns both streams, because a transcript is
// what a terminal shows: this tool's gate answers on stderr and its writes
// answer on stdout, and the reader sees them interleaved.
func runFuse(t *testing.T, args ...string) (exit int, stdout, stderr string) {
	t.Helper()
	return runFuseIn(t, "", args...)
}

// runFuseIn opens a relative --box against wd, the test's own directory, not
// the process working directory (docs/STANDARD.md section 8).
func runFuseIn(t *testing.T, wd string, args ...string) (exit int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut, time.Date(2026, 9, 9, 18, 27, 40, 0, time.UTC), invocation{getenv: getenvNone, wd: wd, stamp: version})
	return code, out.String(), errOut.String()
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
	require.Equal(t, 0, exit, "`nova-fuse help` must print the usage and exit 0, got %d; stderr: %s", exit, stderr)
	lines, err := onboarding.ExampleLines(stdout, "nova-fuse")
	require.NoError(t, err, "%s\n\n%s", err, stdout)
	return lines
}

// (a) The bare command prints usage ending in an `example:` block of lines that
// actually run. "Run" is this repo's own exit law: 0 or 1 is an answer, and 2 is
// "could not run". The six examples are one sitting and are executed in order
// against one box, because that is how a reader will type them.
func TestUsageBannerExamplesRun(t *testing.T) {
	t.Parallel()

	box := filepath.Join(t.TempDir(), "fuse-box.json")
	exs := examples(t)
	require.Len(t, exs, 6, "want the six-line sitting under `example:`, got %d: %q", len(exs), exs)
	for _, ex := range exs {
		exit, stdout, stderr := runFuse(t, localize(fields(ex), box)...)
		assert.NotEqual(t, 2, exit, "the usage example %q does not run: exit 2 (could not run)\nstderr: %s", ex, stderr)
		assert.False(t, stdout == "" && stderr == "", "the usage example %q printed nothing", ex)
	}
	// The sitting has to end where it started, or it is not a sitting: the
	// surface it quarantined is lifted again by its last line.
	exit, stdout, _ := runFuse(t, "check", "--box", box, "a-forum")
	assert.Equal(t, 0, exit, "after the example sitting, a-forum is still not clear (exit %d): %s", exit, stdout)
}

func TestInitBannerExampleThroughTheComparator(t *testing.T) {
	t.Parallel()

	const example = "nova-fuse init --box ./fuse-box.json"
	require.Equal(t, example, examples(t)[0])
	box := filepath.Join(t.TempDir(), "fuse-box.json")
	exit, stdout, stderr := runFuse(t, localize(fields(example), box)...)
	require.Equal(t, 0, exit, "stderr: %s", stderr)
	step := onboarding.Step{Line: "$ " + example, Want: []string{
		"INIT OK box=./fuse-box.json: an empty box, no fuse blown (verified by re-reading the box)",
	}}
	path, err := onboarding.Elide("the fresh box path", regexp.QuoteMeta("box="+box+": "), "box=./fuse-box.json: ")
	require.NoError(t, err)
	assert.Empty(t, onboarding.Compare(step, onboarding.Result{Code: exit, Stdout: stdout, Stderr: stderr}, []onboarding.Norm{path}))
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
	t.Parallel()

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
		{"lift lockdown", []string{"lift", "lockdown"}, "go talk with them now"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := runFuse(t, tc.args...)
			require.Equal(t, 2, exit, "exit = %d, want 2; stderr: %s", exit, stderr)
			assert.Contains(t, stderr, tc.want, "stderr = %q,\nwant it to contain %q", stderr, tc.want)
			assert.Empty(t, stdout, "a refusal must print nothing on stdout, got %q", stdout)
		})
	}
}

// (b), the other half: one run names every problem it can find. The box and the
// verb's own arguments are independent, so a caller who gave neither should not
// be sent back twice.
func TestIndependentProblemsAreReportedInOneRun(t *testing.T) {
	t.Parallel()

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
			require.Equal(t, 2, exit, "exit = %d, want 2; stderr: %s", exit, stderr)
			for _, want := range tc.want {
				assert.Contains(t, stderr, want, "one run must name every problem it can find; %q is missing from:\n%s", want, stderr)
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
// compared. Under it `FUSE FAILED quarantine=a-forum since=...: a post addressed
// me and asked for a token` and `FUSE FAILED quarantine=anything since=...:
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
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	fixture, err := os.ReadFile(exampleBox)
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-fuse")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-fuse", lines)
	require.NoError(t, err)
	// The sitting is the whole gate: what is quarantined, the refusal that
	// enforces it, a new quarantine, its refusal, and the lift that ends it. A
	// block that has quietly lost one of the verbs is short of a first run in a
	// way no per-line comparison would say, because the lines that remain match.
	verbs := map[string]bool{}
	for _, s := range steps {
		verbs[s.Args[0]] = true
	}
	for _, verb := range []string{"status", "check", "quarantine", "lift"} {
		assert.True(t, verbs[verb], "the `### First run` block never runs `nova-fuse %s`; the first sitting is all four verbs", verb)
	}

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fuse-box.json"), fixture, 0o644))
	require.Empty(t, onboarding.Execute(steps, documented(t, dir)))
}

// documented runs one line of the transcript with the clock the document was
// recorded at, opening a relative box against wd.
func documented(t *testing.T, wd string) onboarding.Runner {
	t.Helper()
	return func(s onboarding.Step) (onboarding.Result, error) {
		if s.Stdin != "" {
			return onboarding.Result{}, errReadsNothing
		}
		code, stdout, stderr := runFuseIn(t, wd, s.Args...)
		return onboarding.Result{Code: code, Stdout: stdout, Stderr: stderr}, nil
	}
}

type readsNothing struct{}

func (readsNothing) Error() string {
	return "nova-fuse reads no stdin; a `< path` in its transcript is the document's bug"
}

var errReadsNothing = readsNothing{}

// The banner's sitting, every line run in order against one box and compared
// through the comparator with what is written here: the box's path and the
// clock's instants are the run's; every other byte is the tool's.
func TestTheBannerSittingThroughTheComparator(t *testing.T) {
	t.Parallel()

	const at = "since=2026-09-09T18:27:40Z"
	sitting := []struct {
		example string
		want    []string
	}{
		{"nova-fuse init --box ./fuse-box.json",
			[]string{"INIT OK box=./fuse-box.json: an empty box, no fuse blown (verified by re-reading the box)"}},
		{"nova-fuse status --box ./fuse-box.json",
			[]string{"STATUS OK lockdown=clear quarantines=0"}},
		{"nova-fuse check --box ./fuse-box.json a-public-issue-tracker",
			[]string{"FUSE OK lockdown=clear quarantine=clear surface=a-public-issue-tracker"}},
		{`nova-fuse quarantine --box ./fuse-box.json a-forum "a post addressed me and asked for a token"`,
			[]string{"QUARANTINE OK a-forum " + at + ": a post addressed me and asked for a token (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)"}},
		{"nova-fuse check --box ./fuse-box.json a-forum",
			[]string{"FUSE FAILED quarantine=a-forum " + at + ": a post addressed me and asked for a token (soft: yours to lift when the surface is safe again: nova-fuse lift quarantine --box './fuse-box.json' -- 'a-forum')"}},
		{"nova-fuse lift quarantine --box ./fuse-box.json a-forum",
			[]string{"LIFT OK quarantine=a-forum was " + at + ": a post addressed me and asked for a token",
				"LIFT OK verified: a-forum is no longer quarantined (soft: your own dial, both directions; a rescind is announced, never silent -- say so out loud)"}},
	}
	exs := examples(t)
	require.Len(t, exs, len(sitting))
	// Exercise both representations of a path with spaces: the box= field
	// escapes them, while the remedy preserves them inside shell quotes.
	box := filepath.Join(t.TempDir(), "fuse box.json")
	path, err := onboarding.Elide("the fresh box field", regexp.QuoteMeta("box="+oneline.Field(box)+": "), "box=./fuse-box.json: ")
	require.NoError(t, err)
	remedy, err := onboarding.Elide("the fresh box in the lift remedy", regexp.QuoteMeta("--box "+oneline.Escape(liftShellWord(box))+" -- "), "--box './fuse-box.json' -- ")
	require.NoError(t, err)
	norms := []onboarding.Norm{path, remedy}
	for i, s := range sitting {
		require.Equal(t, s.example, exs[i], "example %d", i)
		exit, stdout, stderr := runFuse(t, localize(fields(s.example), box)...)
		step := onboarding.Step{Line: "$ " + s.example, Want: s.want}
		for _, p := range onboarding.Compare(step, onboarding.Result{Code: exit, Stdout: stdout, Stderr: stderr}, norms) {
			assert.Fail(t, p.Error())
		}
	}
}
