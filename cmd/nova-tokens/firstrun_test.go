package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
)

// ONBOARDING.md, pinned for this binary: the example lines are EXECUTED against the
// fixture, every refusal says what the input WANTS and one run names every independent
// problem, and the TESTS.md transcript is compared against what the tool actually prints.

// fixtureIn is exampleBench with the test moved into it, so a documented ./path is read
// where a reader typing it stands.
func fixtureIn(t *testing.T) string {
	t.Helper()
	dir := exampleBench(t)
	t.Chdir(dir)
	return dir
}

// firstRunStamp is the clock the transcript in TESTS.md was produced under.
var firstRunStamp = time.Date(2026, 9, 11, 23, 55, 2, 0, time.UTC)

// The fixture is what the help's setup line makes, but for its last copy: session's
// transcript is the bench's own window. A line that RUNS answers 0 or 1; exit 2 is "could
// not run", and an example exiting 2 is a broken example.
func TestTheExampleLinesRun(t *testing.T) {
	fixtureIn(t)
	testkit.WriteFile(t, "session.jsonl", testkit.ReadFile(t, filepath.Join("transcripts", "window.jsonl")))
	examples, err := onboarding.ExampleLines(at(firstRunStamp).Do(t, "help").Exit(0).Stdout, "nova-tokens")
	require.NoError(t, err)
	for _, line := range examples {
		r := at(firstRunStamp).Do(t, strings.Fields(line)[1:]...)
		assert.NotEqual(t, 2, r.Code, "the example `%s` could not run: %s", line, r)
	}
}

// One refusal per row. Exit 2 is "could not run", and a refusal writes nothing to stdout.
// There is no quickstart verb, and docs/ONBOARDING.md point 4 wants that said rather than
// guessed at: every verb here needs a path this tool must not invent (an output directory,
// a rules file, a source), so a quickstart would have to write state nobody asked for, in
// a directory nobody named.
func TestEveryRefusalSaysWhatTheInputWantsAndOneRunNamesEveryProblem(t *testing.T) {
	t.Parallel()

	const verbs = "the verbs are fold, report, ledger, sum, check, sources, profiles, session, version; run: nova-tokens help\n"
	for _, c := range []struct {
		name   string
		args   []string
		lines  int      // of stderr, when set
		err    []string // on stderr; with none, stderr is stderr whole
		notErr []string
		stderr string
	}{
		{"three independent problems, three lines, one run", []string{"fold", "--day", "2026-09-11"}, 3,
			[]string{"refusing to guess", "it wants the directory", "it wants a file of", "it wants --claude"}, nil, ""},
		// A flag typo costs ONE line: the verb's flags, the nearest one, and the verb's help
		// as the door, never the banner.
		{"a flag typo costs one line", []string{"fold", "--ou", "x"}, 1,
			[]string{"TOKENS REFUSED: unknown flag --ou; the flags of fold are --all,", "did you mean --out?; run: nova-tokens fold -h"}, []string{"flag provided but not defined"}, ""},
		// An unknown verb, and a bare invocation, name the verbs there are and the door.
		{"an unknown verb", []string{"collate"}, 0, nil, nil, `TOKENS REFUSED: unknown verb "collate"; ` + verbs},
		{"a bare invocation", nil, 0, nil, nil, "TOKENS REFUSED: no verb given; " + verbs},
		{"there is no quickstart verb", []string{"quickstart"}, 0, []string{`unknown verb "quickstart"`}, nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := novaTokens.Do(t, c.args...).Exit(2).Err(c.err...).NotErr(c.notErr...)
			assert.Empty(t, r.Stdout, r)
			if c.lines > 0 {
				assert.Len(t, strings.Split(strings.TrimSuffix(r.Stderr, "\n"), "\n"), c.lines, r)
			}
			if c.err == nil {
				assert.Equal(t, c.stderr, r.Stderr)
			}
		})
	}
	t.Run("the door opens on stdout at exit 0", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, novaTokens.Do(t, "help").Exit(0).Stderr)
	})
	t.Run("the command reference says why there is no quickstart", func(t *testing.T) {
		t.Parallel()
		assert.Contains(t, testkit.ReadFile(t, filepath.Join("..", "..", "docs", "CLI.md")), "no `quickstart`", "docs/STANDARD.md, onboarding point 4")
	})
}

// The `### First run` block of docs/TESTS.md is EXECUTED, not shape-matched:
// every command is run in one sitting, in order, against a COPY of the fixture
// in t.TempDir() (a first run WRITES), and each step's whole output is compared
// line for line -- same number of lines, same lines, same order -- with the
// block written under it, through the one comparator (onboarding.CompareTranscript).
//
// NOTHING IS NORMALISED, and that is a property of this transcript rather than a
// shortcut: the `at=` stamps are driven from run()'s injected clock
// (firstRunStamp, the instant the document was produced at) and the fixture is
// deterministic, so every value on every line reproduces, and the comparator is
// handed no run-owned field: under `go test` the build is `devel`, as written.
//
// The transcript's paths (`./out`, `./repos.tsv`, `./transcripts`, `./bus`) are
// written from the copied fixture's root, where a reader typing them stands, so
// the test moves there rather than rewriting them -- a rewritten path is no
// longer the line the document promised.
func TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine(t *testing.T) {
	lines, err := onboarding.FirstRun(testkit.ReadFile(t, filepath.Join("..", "..", "docs", "TESTS.md")), "nova-tokens")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-tokens", lines)
	require.NoError(t, err)
	// A first run is three commands: fold writes the day, check reads it back, sum reads
	// it a month at a time. A transcript that lost one still matches line for line and is
	// still short of the run a reader is promised.
	require.Len(t, steps, 3, "the `### First run` block's nova-tokens commands")
	fixtureIn(t)
	var results []onboarding.Result
	for _, s := range steps {
		require.Empty(t, s.Stdin, "the documented command reads stdin, and nova-tokens takes none")
		results = append(results, onboarding.Result(at(firstRunStamp).Do(t, s.Args...).Result))
	}
	assert.Empty(t, onboarding.CompareTranscript(steps, results, nil))
}
