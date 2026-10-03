package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// lint --decide prints the brief decision nova-sprint add asks, one LINT DECIDE line
// after the lint's own, and leaves the lint's verdict and exit as they were; with
// --decide-record the decision is recorded, and asked again it is answered from it.
func TestLintDecidePrintsTheBriefDecisionAfterTheLint(t *testing.T) {
	t.Parallel()
	card := filepath.Join("..", "nova-decide", "testdata", "greet.md")
	answers := filepath.Join("..", "nova-decide", "testdata", "brief-answers.json")
	record := filepath.Join(t.TempDir(), "decide", "brief.jsonl")
	plain, plainOut, _ := runSwarm(t, "lint", "--card", card)
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card, "--decide", "--decide-answers", answers, "--decide-record", record)
	assert.Equal(t, plain, exit, "the decision never changes the lint's exit: %s", stderr)
	raw, err := os.ReadFile(card)
	require.NoError(t, err)
	want := "LINT DECIDE card=greet.md op=" + decide.BriefOp("greet", strings.TrimSuffix(string(raw), "\n")) +
		" p_converges=0.72 minutes=under-10 failed=- uncalibrated=true recorded=new\n"
	assert.Equal(t, plainOut+want, stdout, "the lint's own lines, then the decision's")
	_, stdout, _ = runSwarm(t, "lint", "--card", card, "--decide", "--decide-answers", answers, "--decide-record", record)
	assert.True(t, strings.HasSuffix(stdout, "recorded=existing\n"), stdout)
	_, stdout, _ = runSwarm(t, "lint", "--card", card, "--decide", "--decide-answers", answers)
	assert.True(t, strings.HasSuffix(stdout, "failed=- uncalibrated=true recorded=no\n"), "with no record nothing is recorded: %s", stdout)
}

// --decide with no key and no answers file is refused naming nova-secrets exec, and
// with --decide-answers no key is wanted; the two decide flags without --decide are
// refused; --decide over a launcher script is refused with the other card flags.
func TestLintDecideRefusals(t *testing.T) {
	t.Parallel()
	card := filepath.Join("..", "nova-decide", "testdata", "greet.md")
	var stdout, stderr bytes.Buffer
	noKey := func(string) string { return "" }
	assert.Equal(t, 2, cmdLint([]string{"--card", card, "--decide"}, &stdout, &stderr, noKey, time.Time{}))
	assert.Equal(t, "nova-swarm lint: --decide: Jev is asked with JEV_API_KEY, which this environment does not hold; run: nova-secrets exec --only JEV_API_KEY -- nova-swarm lint --card "+card+" --decide, or give --decide-answers <file> (no network, no key)\n", stderr.String())
	assert.Empty(t, stdout.String(), "a refused decision lints nothing")
	stdout.Reset()
	answers := filepath.Join("..", "nova-decide", "testdata", "brief-answers.json")
	cmdLint([]string{"--card", card, "--decide", "--decide-answers", answers}, &stdout, &stderr, noKey, time.Time{})
	assert.Contains(t, stdout.String(), "LINT DECIDE card=greet.md ")

	exit, _, errs := runSwarm(t, "lint", "--card", card, "--decide-record", filepath.Join(t.TempDir(), "r.jsonl"))
	assert.Equal(t, 2, exit)
	assert.Contains(t, errs, "--decide-answers and --decide-record go with --decide")
	script := filepath.Join(t.TempDir(), "launch.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/bash\necho hi\n"), 0o755))
	exit, _, errs = runSwarm(t, "lint", "--fleet", script, "--decide")
	assert.Equal(t, 2, exit)
	assert.Contains(t, errs, "--typed, --trust, --lineup and --decide are card checks")
}
