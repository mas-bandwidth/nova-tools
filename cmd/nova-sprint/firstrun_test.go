package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// firstrun_test.go pins the onboarding standard for this binary: the
// docs/TESTS.md `### First run` transcript is RUN, line for line and in order,
// through the one comparator (onboarding.CompareTranscript, SPEC-TOOLWORK.md
// documents rule 2). It is the twin's card flow (twinSteps, the flow
// nova-sprint help shows, up to accept): each step is its own process over one
// twin file, with the environment the help says to export, so the unit tier
// runs it with no Redis.
//
// NOTHING IS NORMALISED: every value on every line reproduces (a twin counts
// its operation ids), so the comparator is handed no field of the
// onboarding.Volatile table. The last two steps of the help's flow, merge and
// where, print the time a finished sprint took and the instant, which are the
// clock's; twin_test.go runs them.

func TestTheFirstRunTranscriptRunsOverATwin(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-sprint")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-sprint", lines)
	if err != nil {
		t.Fatal(err)
	}
	// the transcript is the help's flow, verb for verb, to the card accepted
	if want := len(twinSteps) - 2; len(steps) != want {
		t.Fatalf("the transcript holds %d commands; the help's flow to accept is %d", len(steps), want)
	}
	for i, s := range steps {
		if want := "$ " + twinSteps[i]; s.Line != want {
			t.Errorf("command %d of the transcript is %q, the help's flow has %q", i+1, s.Line, want)
		}
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
