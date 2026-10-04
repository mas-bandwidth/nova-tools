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
// EXECUTED, every command in order on one harness, and each command's whole
// output is compared with the block under it by the one comparator,
// onboarding.CompareTranscript (docs/SPEC-TOOLWORK.md documents rule 2). The
// banner's `example:` lines are the same commands, so the one sitting covers
// both promises: a first run that needs no database and no secret, every
// verb on a --file store in the test's own directory.
//
// The history's at= is the run's instant, the one value normalised.

// firstRun is the banner's example: block, named in a function the test calls
// so the pasted-examples rule (internal/ci, SPEC-TOOLWORK.md documents rule 6)
// reads the command text.
func firstRun() []string {
	return []string{
		"nova-config migrate --file try.json",
		"nova-config machine add m1 --user nova --seat s1 --slots 8 --width 4 --as a1 --file try.json",
		"nova-config machine set m1 --width 6 --as a1 --file try.json",
		"nova-config machine list --file try.json",
		"nova-config machine history m1 --file try.json",
	}
}

func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	documentedExamples := firstRun()
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
	h := newHarness()
	h.dir = t.TempDir()
	var banner bytes.Buffer
	codeCheck60 := run([]string{"help"}, &banner, &banner, h.deps())
	require.Zero(t, codeCheck60, "help exits nonzero: exit %d", codeCheck60)
	examples, err := onboarding.ExampleLines(banner.String(), "nova-config")
	require.NoError(t, err)
	require.Equal(t, strings.Join(documentedExamples, "\n"), strings.Join(examples, "\n"), "the banner's examples and this test's examples are one list")
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		var out, errb bytes.Buffer
		code := run(s.Args, &out, &errb, h.deps())
		res := onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
		assert.Zero(t, res.Code, "%s exits %d; a first run that needs no store answers 0: %s", s.Line, res.Code, res.Stderr)
		got = append(got, res)
	}
	assert.Empty(t, onboarding.CompareTranscript(steps, got, []onboarding.Field{{Name: "at"}}), "the documented transcript matches")
	assert.Equal(t, len(steps), h.opens, "each line opened the one store, the --file")
	assert.Zero(t, h.redis.opens, "a first run opens no Redis")
	assert.FileExists(t, filepath.Join(h.dir, "try.json"), "the first run's rows are in the one file it names")
}
