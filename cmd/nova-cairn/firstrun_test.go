// First-run tests for nova-cairn: the usage banner's `example:` block and the
// docs/TESTS.md `## nova-cairn` transcript are RUN here rather than read, and
// the transcript is compared line for line through the one comparator,
// onboarding.CompareTranscript (docs/SPEC-TOOLWORK.md documents rule 3). An
// example that has drifted out of the flag set teaches the wrong invocation
// to exactly the reader who cannot tell, and a transcript line no run prints
// is a promise the tool never made.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// usageExamples returns the command lines under the banner's `example:`
// heading. It asks for the banner, because a bare invocation is a refusal
// and this reads what is behind the door the refusal names.
func usageExamples(t *testing.T) []string {
	t.Helper()
	examples, err := onboarding.ExampleLines(cli.OK(t, "help").Stdout, "nova-cairn")
	require.NoError(t, err)
	return examples
}

// The usage banner ends in one example per verb, in the order a first run
// types them: the append needs the record the open created. Each runs, in one
// store, and its whole output is compared through the one comparator. The open
// and the append fix the clock with `--now` so the stamps they print reproduce;
// the index and the receipt read the stamp the append wrote. The documented
// `./cairns` is swapped for a real directory by whole field, so a store path
// this OS spells with a backslash reaches the tool as one argument instead of
// becoming escapes the shared splitter refuses.
func TestUsageBannerExamplesRun(t *testing.T) {
	t.Parallel()

	sitting := []struct {
		example string
		now     string
		want    []string
	}{
		{"nova-cairn open --store ./cairns --session s1 --publish manual",
			"2026-01-01T00:00:00Z",
			[]string{"OPEN OK session=s1 store=./cairns source=- publish=manual stamp=2026-01-01T00:00:00Z"}},
		{`nova-cairn append --store ./cairns --session s1 --entry e1 --text "the words to keep"`,
			"2026-01-01T00:00:00Z",
			[]string{"APPEND OK session=s1 entry=e1 source=- persisted=true published=false publish=manual duplicate=false stamp=2026-01-01T00:00:00Z"}},
		{"nova-cairn index --store ./cairns",
			"",
			[]string{"INDEX OK sessions=1 entries=1", "INDEX SESSION session=s1 publish=manual opened=2026-01-01T00:00:00Z entries=1", "INDEX ENTRY session=s1 entry=e1 stamp=2026-01-01T00:00:00Z bytes=17 source=-"}},
		{"nova-cairn receipt --store ./cairns --session s1 --entry e1 --text",
			"",
			[]string{`RECEIPT OK session=s1 entry=e1 stamp=2026-01-01T00:00:00Z bytes=17 source=- persisted=true published=false publish=manual text="the words to keep"`}},
	}
	examples := usageExamples(t)
	require.Len(t, examples, len(sitting), "want an open, an append, an index and a receipt example under `example:`, got %q", examples)
	// One store for the whole first run: the examples are a sitting, not four.
	store := filepath.Join(t.TempDir(), "cairns")
	steps := make([]onboarding.Step, 0, len(sitting))
	for i, s := range sitting {
		require.Equal(t, s.example, examples[i], "example %d", i)
		fields, err := onboarding.SplitShell(strings.ReplaceAll(s.example, "./cairns", store))
		require.NoError(t, err, "cannot split the usage example %q", s.example)
		args := fields[1:]
		if s.now != "" {
			args = append(args, "--now", s.now)
		}
		steps = append(steps, onboarding.Step{Line: "$ " + s.example, Args: args, Want: s.want})
	}
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		got = append(got, onboarding.Result(cli.Run(s.Args...)))
	}
	volatile := []onboarding.Field{{Name: "tmpdir", Doc: "./cairns", Run: store}}
	for _, p := range onboarding.CompareTranscript(steps, got, volatile) {
		assert.Fail(t, p.Error())
	}
}

// The `## nova-cairn` section of docs/TESTS.md is EXECUTED: every documented
// command is run, in order, in one directory, and its whole output is compared
// with the block written under it -- same number of lines, same lines, same
// order -- through the one comparator.
//
// NOTHING IS NORMALISED but the store path, and that is a property of this
// transcript rather than a shortcut. The stamps come from `--now`, the ids are
// named on the command line, and the byte count is of the text typed there, so
// every other value on every line reproduces. onboarding.CompareTranscript is
// told so by being handed only the `tmpdir` field of the onboarding.Volatile
// table, and it says as much under any line that disagrees.
//
// The documented `./cairns` is relative and the tool PRINTS IT BACK on every
// line. It stands for a directory under this test's own t.TempDir(), named to
// the comparator through `tmpdir`, so the sitting runs in parallel without
// moving the process working directory: the printed path reduces back to the
// spelling the document promises (the per-test seam the serial-tests ledger
// names for a path: t.TempDir).
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-cairn")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-cairn", lines)
	require.NoError(t, err)
	// The sitting is the whole tool: a record opened, a line appended to it,
	// the index that shows it and the receipt that proves it. A block that has
	// quietly lost one of the four verbs is short of a first run, and no
	// per-line comparison would say so -- the lines that remain would match.
	var verbs []string
	for _, s := range steps {
		verbs = append(verbs, s.Args[0])
	}
	assert.Subset(t, verbs, []string{"open", "append", "index", "receipt"}, "the `### First run` block never runs one of the four verbs; the first sitting is all four")

	// ONE store for the whole sitting: the transcript opens a record and then
	// appends to it, and a fresh directory per line would unmake that. It lives
	// under t.TempDir() so the sitting needs no process working directory.
	store := filepath.Join(t.TempDir(), "cairns")
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		res, err := runDocumented(store, s)
		require.NoError(t, err, "the documented command\n  %s\ncould not be run: %v", s.Line, err)
		got = append(got, res)
	}
	volatile := []onboarding.Field{{Name: "tmpdir", Doc: "./cairns", Run: store}}
	for _, p := range onboarding.CompareTranscript(steps, got, volatile) {
		assert.Fail(t, p.Error())
	}
}

// runDocumented calls this binary's own entry point with the documented
// arguments, resolving the one relative path the sitting names (./cairns) under
// store, this test's own directory, so the record is written there instead of in
// the process working directory. nova-cairn's first run reads nothing on stdin.
func runDocumented(store string, s onboarding.Step) (onboarding.Result, error) {
	if s.Stdin != "" {
		return onboarding.Result{}, errReadsNothing
	}
	args := make([]string, len(s.Args))
	for i, a := range s.Args {
		if a == "./cairns" {
			a = store
		}
		args[i] = a
	}
	return onboarding.Result(cli.Run(args...)), nil
}

type readsNothing struct{}

func (readsNothing) Error() string {
	return "nova-cairn's first run reads no stdin; a `< path` in its transcript is the document's bug"
}

var errReadsNothing = readsNothing{}
