package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFirstRunTranscriptIsWhatTheToolPrints runs every `$` line of the
// `### First run` under `## nova-redis` in docs/TESTS.md through run() and
// compares what it prints with the shared comparator (onboarding.Execute ->
// onboarding.Compare, docs/SPEC-TOOLWORK.md documents rule 2), line for line. Reaching the
// store fails the test: connect reads the environment before it opens
// anything, and that first read stops the test before any dial. Both lines are
// refusals made before the instance is dialled, which is the promise the
// transcript documents (their --addr is 127.0.0.1:6379, which is never to be
// written to from a test).
func TestFirstRunTranscriptIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-redis")
	require.NoError(t, err, err)
	steps, err := onboarding.Steps("nova-redis", lines)
	require.NoError(t, err, err)
	require.NotEmpty(t, steps, "the nova-redis first run holds no `$ nova-redis` line; this test checked nothing")
	d := deps{
		now: func() time.Time { return time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC) },
		getenv: func(k string) string {
			require.FailNowf(t, "", "a first-run refusal reached connect (it read %s); a refused write must never reach the instance", k)
			return ""
		},
	}
	runner := func(s onboarding.Step) (onboarding.Result, error) {
		var out, errb bytes.Buffer
		code := run(s.Args, &out, &errb, d)
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
	}
	for _, p := range onboarding.Execute(steps, runner) {
		assert.Failf(t, "", "docs/TESTS.md: %s", p)
	}
}
