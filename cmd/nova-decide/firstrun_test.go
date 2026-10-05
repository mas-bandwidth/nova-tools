// First-run tests for nova-decide: the banner's `example:` block and the
// docs/TESTS.md `### First run` transcript are RUN here, as one sitting, with
// the fixed backend, so they need no network and no key.
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

// emptyDir is a fresh directory holding what the help's `setup:` lines write:
// each `printf '<text>' > <file>` line is run, so the example block reads
// files the banner itself made. The returned function runs one example line
// there, a bare file name standing for its path in that directory.
func emptyDir(t *testing.T, help string) func(args []string) onboarding.Result {
	dir := t.TempDir()
	_, tail, found := strings.Cut(help, "setup:")
	require.True(t, found, "the help has no setup: block")
	n := 0
	for _, line := range strings.Split(tail, "\n")[1:] {
		fields, err := onboarding.SplitShell(strings.TrimSpace(line))
		if err != nil || len(fields) != 4 || fields[0] != "printf" || fields[2] != ">" {
			break
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, fields[3]), []byte(fields[1]), 0o644))
		n++
	}
	require.Equal(t, 3, n, "setup: writes schema.json, state.txt and answers.json, one printf each")
	return func(args []string) onboarding.Result {
		local := make([]string, len(args))
		for i, a := range args {
			local[i] = a
			if _, err := os.Stat(filepath.Join(dir, a)); err == nil || a == "decisions.jsonl" {
				local[i] = filepath.Join(dir, a)
			}
		}
		return onboarding.Result(cli.Run(local...))
	}
}

// The usage banner's example block is ask, outcome and findings, in that order,
// and runs as printed in an empty directory after the setup: lines, each exiting 0.
func TestUsageBannerExamplesRun(t *testing.T) {
	t.Parallel()
	help := cli.OK(t, "help").Stdout
	examples, err := onboarding.ExampleLines(help, "nova-decide")
	require.NoError(t, err)
	verbs := []string{"ask", "outcome", "findings"}
	require.Len(t, examples, len(verbs), "want one example of each of %v under `example:`, got %q", verbs, examples)
	run := emptyDir(t, help)
	for i, verb := range verbs {
		fields, err := onboarding.SplitShell(examples[i])
		require.NoError(t, err)
		require.Equal(t, verb, fields[1], "example %d", i)
		res := run(fields[1:])
		assert.Equal(t, 0, res.Code, "the example %q exits %d: %s", examples[i], res.Code, res.Stderr)
	}
}

// The `### First run` block of docs/TESTS.md is EXECUTED, every command in
// order, and its whole output compared with the block by the one comparator;
// nothing is normalised, because every id comes from --op and no line prints a
// stamp or a path.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	// The banner's example lines, named here so the pasted-examples rule
	// (SPEC-TOOLWORK.md documents rule 6) reads the command text in this test;
	// the transcript runs the same lines.
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
	// The banner's block runs in an empty directory (TestUsageBannerExamplesRun);
	// each other verb's example is the checkout-root line its own help prints.
	for _, line := range documentedExamples {
		verb := strings.Fields(line)[1]
		if verb == "ask" || verb == "outcome" || verb == "findings" {
			continue
		}
		assert.Contains(t, cli.OK(t, "help", verb).Stdout, "nova-decide "+line[len("nova-decide "):], "the help of %s does not print its example", verb)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-decide")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-decide", lines)
	require.NoError(t, err)
	var commands []string
	for _, s := range steps {
		commands = append(commands, s.Line)
	}
	require.Len(t, commands, len(documentedExamples), "the transcript runs %q", commands)
	run := sitting(t)
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		got = append(got, run(s.Args))
	}
	assert.Empty(t, onboarding.CompareTranscript(steps, got, nil))
}
