// First-run tests for nova-decide: the docs/TESTS.md `### First run`
// transcript is RUN here, as one sitting, with the fixed backend, so it needs
// no network and no key. The banner's own `example:` block runs as printed,
// from an empty directory, in verbhelp_test.go.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sitting runs documented lines from a checkout root: the fixture paths are
// the checkout's, and the record the first run writes (./decisions.jsonl) is
// a file in the test's own directory.
func sitting(t *testing.T) func(args []string) onboarding.Result {
	dir := t.TempDir()
	testdata, err := filepath.Abs("testdata")
	require.NoError(t, err)
	stand := strings.NewReplacer("./cmd/nova-decide/testdata/", testdata+"/", "./decisions.jsonl", filepath.Join(dir, "decisions.jsonl"))
	return func(args []string) onboarding.Result {
		local := make([]string, len(args))
		for i, a := range args {
			local[i] = stand.Replace(a)
		}
		return onboarding.Result(cli.Run(local...))
	}
}

// The `### First run` block of docs/TESTS.md is EXECUTED, every command in
// order, and its whole output compared with the block by the one comparator;
// nothing is normalised, because every id comes from --op and no line prints a
// stamp or a path. The banner's example block is the setup-and-first-run a
// stranger pastes (verbhelp_test.go holds it); these ten lines are the
// documented sitting over the checkout's own fixtures, and they are named
// here so the pasted-examples rule (SPEC-TOOLWORK.md documents rule 6) reads
// the command text in this test.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	documentedExamples := []string{
		"nova-decide ask --schema ./cmd/nova-decide/testdata/schema.json --state ./cmd/nova-decide/testdata/state.txt --backend fixed --answers ./cmd/nova-decide/testdata/answers.json --record ./decisions.jsonl --op first",
		"nova-decide read --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --backend fixed --answers ./cmd/nova-decide/testdata/read-answers.json --record ./decisions.jsonl --op card-1",
		"nova-decide score --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --backend fixed --answers ./cmd/nova-decide/testdata/score-answers.json --record ./decisions.jsonl --op card-1@landed@0123456789ab",
		`nova-decide attempt --brief ./cmd/nova-decide/testdata/card.md --result ./cmd/nova-decide/testdata/result.md --reason "verdict not-done: tests red in internal/decide" --backend fixed --answers ./cmd/nova-decide/testdata/attempt-answers.json --record ./decisions.jsonl --op c1@1`,
		"nova-decide grade --brief ./cmd/nova-decide/testdata/card.md --backend fixed --answers ./cmd/nova-decide/testdata/grade-answers.json --record ./decisions.jsonl --op c1@grade",
		"nova-decide gate --output ./cmd/nova-decide/testdata/gate-output.txt --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --base-red TestPortInUse --backend fixed --answers ./cmd/nova-decide/testdata/gate-answers.json --record ./decisions.jsonl --op c1@1@gate",
		"nova-decide brief --card ./cmd/nova-decide/testdata/greet.md --backend fixed --answers ./cmd/nova-decide/testdata/brief-answers.json --record ./decisions.jsonl",
		`nova-decide outcome --record ./decisions.jsonl --id card-1 --label ok --note "the review found nothing"`,
		"nova-decide calibrate --record ./cmd/nova-decide/testdata/record.jsonl --decision read --question defect --positive wrong --negative ok",
		"nova-decide findings --record ./cmd/nova-decide/testdata/record.jsonl --since 2026-10-01",
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-decide")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-decide", lines)
	require.NoError(t, err)
	var commands []string
	for _, s := range steps {
		commands = append(commands, strings.TrimPrefix(s.Line, "$ "))
	}
	require.Equal(t, documentedExamples, commands, "the transcript runs the documented examples")
	run := sitting(t)
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		got = append(got, run(s.Args))
	}
	assert.Empty(t, onboarding.CompareTranscript(steps, got, nil))
}
