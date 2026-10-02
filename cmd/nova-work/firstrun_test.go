package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTESTSFirstRunIsWhatTheToolPrints: the `### First run` block of
// docs/TESTS.md is EXECUTED, every command in order, against the recorded
// conversation with GitHub the other tests use (internal/workgh/testdata/
// reliable: one public repository of twenty issues, read at fifteen a page), and
// the whole output is compared by the one comparator. The banner's `example:`
// block is the same three commands (docs/ONBOARDING.md point 6), so one sitting
// keeps both promises.
//
// $ORG and $REPO are the reader's: the test stands them for the recording's
// names on the command line, and the comparator's `recorded` entry writes the
// recording's names back as $ORG and $REPO where the tool prints them.
// ./tree.lisp is a file in a directory of the test's own, and ./gh is where
// the test says gh was found; every query is answered by the strict replay of
// the recording, with no process started (the gh adapter itself is
// internal/workgh's functional test).
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	// The banner's example lines, named in this test's own body so the
	// pasted-examples rule (internal/ci, SPEC-TOOLWORK.md documents rule 6) reads
	// the command text here; the transcript holds the same lines.
	documentedExamples := []string{
		"nova-work import --org $ORG --repo $ORG/$REPO --page-size 15 --dry-run",
		"nova-work import --org $ORG --repo $ORG/$REPO --page-size 15 --out ./tree.lisp",
		"nova-work verify --tree ./tree.lisp --repo $ORG/$REPO --page-size 15",
	}
	examples, err := onboarding.ExampleLines(workTool(realGitHub()).Banner(), "nova-work")
	require.NoError(t, err)
	require.Equal(t, strings.Join(documentedExamples, "\n"), strings.Join(examples, "\n"), "the banner's examples are %q, this test names %q", examples, documentedExamples)

	raw := testkit.ReadFile(t, filepath.Join("..", "..", "docs", "TESTS.md"))
	lines, err := onboarding.FirstRun(raw, "nova-work")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-work", lines)
	require.NoError(t, err)
	var commands []string
	for _, s := range steps {
		commands = append(commands, "nova-work "+strings.Join(s.Args, " "))
	}
	require.Equal(t, strings.Join(documentedExamples, "\n"), strings.Join(commands, "\n"), "the transcript runs %q and the banner's examples are %q; they are one list", commands, documentedExamples)

	const org, repo = "mas-bandwidth", "reliable" // the recording's
	dir := t.TempDir()
	stand := strings.NewReplacer("$ORG", org, "$REPO", repo, "./", dir+"/")
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		args := []string{}
		for _, a := range s.Args {
			args = append(args, stand.Replace(a))
		}
		// Each line is its own conversation with GitHub, from the recording's first call.
		res := workMain(recorded(t, filepath.Join(dir, "gh"))).Run(args...)
		assert.Equal(t, 0, res.Code, "the documented command %s exits %d; stderr: %s", s.Line, res.Code, res.Stderr)
		got = append(got, onboarding.Result{Code: res.Code, Stdout: res.Stdout, Stderr: res.Stderr})
	}
	volatile := []onboarding.Field{
		{Name: "tmpdir", Doc: ".", Run: dir},
		{Name: "recorded", Doc: "$ORG", Run: org},
		{Name: "recorded", Doc: "$REPO", Run: repo},
	}
	assert.Empty(t, onboarding.CompareTranscript(steps, got, volatile))
}
