package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// firstrun_test.go pins the onboarding standard for this binary: the
// docs/TESTS.md `### First run` transcript is RUN, line for line and in order,
// through the one comparator (onboarding.CompareTranscript, SPEC-TOOLWORK.md
// documents rule 2). It is the twin's card flow (twinSteps, the flow
// nova-sprint help shows, up to the merge): each step is its own process over one
// twin file, with the environment the help says to export, so the unit tier
// runs it with no Redis.
//
// NOTHING IS NORMALISED: every value on every line reproduces (a twin counts
// its operation ids), so the comparator is handed no field of the
// onboarding.Volatile table. The last two steps of the help's flow, the tick
// after the merge and where, print the time a finished sprint took and the
// instant, which are the clock's; twin_test.go runs them.

func TestTheFirstRunTranscriptRunsOverATwin(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-sprint")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-sprint", lines)
	require.NoError(t, err)
	// the transcript is the help's flow, verb for verb, to the card merged
	require.Len(t, steps, len(twinSteps)-2, "the transcript holds %d commands; the help's flow to the merge is %d", len(steps), len(twinSteps)-2)
	for i, s := range steps {
		assert.Equal(t, "$ "+twinSteps[i], s.Line, "command %d of the transcript", i+1)
	}
	file := filepath.Join(t.TempDir(), "sprint.twin")
	env := map[string]string{"NOVA_SPRINT_REDIS": "mem:" + file, "NOVA_SPRINT_ACTOR": "boss"}
	var got []onboarding.Result
	for _, s := range steps {
		a := newApp(func(k string) string { return env[k] })
		var out, errb bytes.Buffer
		code := a.run(s.Args, &out, &errb)
		a.close()
		got = append(got, onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()})
	}
	for _, p := range onboarding.CompareTranscript(steps, got, nil) {
		t.Error(p)
	}
}
