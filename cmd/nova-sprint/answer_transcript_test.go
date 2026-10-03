package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// The docs/TESTS.md `### Answered by nova-decide` transcript is run, line for line, over
// a fresh twin: the record is kept in a temporary directory and printed back as the
// ./judgment.jsonl the transcript names, and the fixed backend's answers are this
// package's testdata, which the transcript names from a checkout root.
func TestTheAnswerTranscriptRunsOverATwin(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.Transcript(string(raw), "nova-sprint", "Answered by nova-decide")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-sprint", lines)
	require.NoError(t, err)
	dir := t.TempDir()
	record := filepath.Join(dir, "judgment.jsonl")
	env := map[string]string{"NOVA_SPRINT_REDIS": "mem:" + filepath.Join(dir, "sprint.twin"), "NOVA_SPRINT_ACTOR": "boss"}
	var got []onboarding.Result
	for _, s := range steps {
		args := make([]string, len(s.Args))
		for i, a := range s.Args {
			args[i] = strings.NewReplacer("./cmd/nova-sprint/testdata/", "testdata/", "./judgment.jsonl", record).Replace(a)
		}
		a := newApp(func(k string) string { return env[k] })
		var out, errb bytes.Buffer
		code := a.run(args, &out, &errb)
		a.close()
		got = append(got, onboarding.Result{Code: code, Stdout: strings.ReplaceAll(out.String(), record, "./judgment.jsonl"), Stderr: errb.String()})
	}
	for _, p := range onboarding.CompareTranscript(steps, got, nil) {
		t.Error(p)
	}
	ds, err := os.ReadFile(record)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(string(ds), "\n"), "the first pass recorded two decisions, the second applied them from the record")
}
