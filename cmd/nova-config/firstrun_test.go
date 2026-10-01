package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// firstrun_test.go pins the onboarding standard for this binary
// (docs/ONBOARDING.md): the `### First run` block of docs/TESTS.md is
// EXECUTED, every command in order, and each command's whole output is
// compared with the block under it by the one comparator,
// onboarding.CompareTranscript (docs/SPEC-TOOLWORK.md documents rule 2). The
// banner's `example:` lines are the same three commands, so the one sitting
// covers both promises: a first run that needs no store and no secret.
//
// Nothing is normalised: `kinds` and `migrate --print` read the descriptors
// and the embedded migrations, so every value reproduces on every bench.

func runDocumented(s onboarding.Step) (onboarding.Result, error) {
	var out, errb bytes.Buffer
	code := run(s.Args, &out, &errb, newHarness().deps())
	return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
}

func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	// The banner's example lines, named in this test's own body so the
	// pasted-examples rule (internal/ci, SPEC-TOOLWORK.md documents rule 6) reads
	// the command text here; the transcript below holds the same lines.
	documentedExamples := []string{
		"nova-config kinds",
		"nova-config migrate --print",
		"nova-config machine add -h",
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(root, "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-config")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-config", lines)
	require.NoError(t, err)
	require.NotEmpty(t, steps, "the `### First run` block holds no nova-config command; this test would pass by running nothing")
	// The transcript's commands are the banner's examples, in order: one
	// sitting proves both.
	var commands []string
	for _, s := range steps {
		commands = append(commands, strings.TrimPrefix(s.Line, "$ "))
	}
	require.Equal(t, strings.Join(documentedExamples, "\n"), strings.Join(commands, "\n"), "the transcript's commands and the banner's examples are one list")
	var banner bytes.Buffer
	if code := run([]string{"help"}, &banner, &banner, newHarness().deps()); code != 0 {
		require.FailNow(t, "help exits nonzero", "exit %d", code)
	}
	examples, err := onboarding.ExampleLines(banner.String(), "nova-config")
	require.NoError(t, err)
	require.Equal(t, strings.Join(documentedExamples, "\n"), strings.Join(examples, "\n"), "the banner's examples and this test's examples are one list")
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		res, err := runDocumented(s)
		require.NoError(t, err, "the documented command\n  %s\ncould not be run", s.Line)
		if res.Code != 0 {
			assert.Fail(t, "the documented command exits nonzero", "%s exits %d; a first run that needs no store answers 0", s.Line, res.Code)
		}
		got = append(got, res)
	}
	for _, p := range onboarding.CompareTranscript(steps, got, nil) {
		assert.Fail(t, p.String())
	}
}
