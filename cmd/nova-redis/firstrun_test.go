package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/redis/go-redis/v9"
)

// TestFirstRunTranscriptIsWhatTheToolPrints runs every `$` line of the
// `### First run` under `## nova-redis` in docs/TESTS.md through run() and
// compares what it prints with the shared comparator (onboarding.Execute ->
// onboarding.Compare, docs/SPEC-TOOLWORK.md §3), line for line. The dial seam
// fails the test if it is reached: both lines are refusals made before the
// instance is dialled, which is the promise the transcript documents.
func TestFirstRunTranscriptIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-redis")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-redis", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatal("the nova-redis first run holds no `$ nova-redis` line; this test checked nothing")
	}
	d := deps{
		now: func() time.Time { return time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC) },
		dial: func(addr, password string) redis.Cmdable {
			t.Fatalf("a first-run refusal dialled %s; a refused write must never reach the instance", addr)
			return nil
		},
		getenv: func(string) string { return "" },
	}
	runner := func(s onboarding.Step) (onboarding.Result, error) {
		var out, errb bytes.Buffer
		code := run(s.Args, &out, &errb, d)
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
	}
	for _, p := range onboarding.Execute(steps, runner) {
		t.Errorf("docs/TESTS.md: %s", p)
	}
}
