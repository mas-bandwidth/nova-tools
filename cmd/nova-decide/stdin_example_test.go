package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecideVerbsAcceptStdinStream(t *testing.T) {
	t.Parallel()
	card := testkit.ReadFile(t, td+"card.md")
	diff := testkit.ReadFile(t, td+"card.diff")
	rule := "Do not weaken the example test.\n"
	gateOutput := testkit.ReadFile(t, td+"gate-output.txt")
	gateCard := "PATHS: pkg/example.go\n"
	gateDiff := "diff --git a/pkg/example.go b/pkg/example.go\n--- a/pkg/example.go\n+++ b/pkg/example.go\n@@ -1 +1 @@\n-old\n+new\n"

	cases := []struct {
		name, stdin, stdinInput, decision, wantState string
		args                                         func(record string) []string
	}{
		{name: "ask state", stdin: "a state supplied on stdin\n", stdinInput: "state", decision: "reply", wantState: "a state supplied on stdin",
			args: func(record string) []string {
				return []string{"ask", "--schema", td + "schema.json", "--state", "-", "--backend", "fixed", "--answers", td + "answers.json", "--record", record, "--op", "stdin-ask"}
			}},
		{name: "read card", stdin: card, stdinInput: "card", decision: decide.ReadName, wantState: card,
			args: func(record string) []string {
				return []string{"read", "--card", "-", "--diff", td + "card.diff", "--backend", "fixed", "--answers", td + "read-answers.json", "--record", record, "--op", "stdin-read-card"}
			}},
		{name: "read diff", stdin: diff, stdinInput: "diff", decision: decide.ReadName, wantState: diff,
			args: func(record string) []string {
				return []string{"read", "--card", td + "card.md", "--diff", "-", "--backend", "fixed", "--answers", td + "read-answers.json", "--record", record, "--op", "stdin-read-diff"}
			}},
		{name: "read rule", stdin: rule, stdinInput: "rule", decision: decide.ReadName, wantState: rule,
			args: func(record string) []string {
				return []string{"read", "--card", td + "card.md", "--diff", td + "card.diff", "--rule", "-", "--backend", "fixed", "--answers", td + "read-answers.json", "--record", record, "--op", "stdin-read-rule"}
			}},
		{name: "score card", stdin: card, stdinInput: "card", decision: decide.ScoreName, wantState: card,
			args: func(record string) []string {
				return []string{"score", "--card", "-", "--diff", td + "card.diff", "--backend", "fixed", "--answers", td + "score-answers.json", "--record", record, "--op", "stdin-score-card"}
			}},
		{name: "score diff", stdin: diff, stdinInput: "diff", decision: decide.ScoreName, wantState: diff,
			args: func(record string) []string {
				return []string{"score", "--card", td + "card.md", "--diff", "-", "--backend", "fixed", "--answers", td + "score-answers.json", "--record", record, "--op", "stdin-score-diff"}
			}},
		{name: "gate output", stdin: gateOutput, decision: decide.GateName, wantState: "TestPortInUse",
			args: func(record string) []string {
				return []string{"gate", "--output", "-", "--card", td + "card.md", "--backend", "fixed", "--answers", td + "gate-answers.json", "--record", record, "--op", "stdin-gate-output"}
			}},
		{name: "gate card", stdin: gateCard, decision: decide.GateName, wantState: "pkg/example.go",
			args: func(record string) []string {
				return []string{"gate", "--output", td + "gate-output.txt", "--card", "-", "--backend", "fixed", "--answers", td + "gate-answers.json", "--record", record, "--op", "stdin-gate-card"}
			}},
		{name: "gate diff", stdin: gateDiff, decision: decide.GateName, wantState: "pkg/example.go",
			args: func(record string) []string {
				return []string{"gate", "--output", td + "gate-output.txt", "--card", td + "card.md", "--diff", "-", "--backend", "fixed", "--answers", td + "gate-answers.json", "--record", record, "--op", "stdin-gate-diff"}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			record := filepath.Join(dir, "decisions.jsonl")
			r := cli.DoIn(t, tc.stdin, tc.args(record)...).Exit(0).Out("OK")
			assert.Empty(t, r.Stderr)
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			names := make([]string, len(entries))
			for i := range entries {
				names[i] = entries[i].Name()
			}
			require.Equal(t, []string{"decisions.jsonl"}, names, "the command writes no stdin copy")
			ds, err := decide.Load(record)
			require.NoError(t, err)
			require.NotEmpty(t, ds)
			found := false
			for _, d := range ds {
				if d.Decision != tc.decision {
					continue
				}
				found = true
				assert.Contains(t, d.State, strings.TrimRight(tc.wantState, "\n"))
				if tc.stdinInput != "" {
					assert.Equal(t, "-", d.Inputs[tc.stdinInput])
					assert.Equal(t, decide.Sum([]byte(tc.stdin)), d.Inputs[tc.stdinInput+"_sha256"])
				}
			}
			assert.True(t, found, "record contains the requested %s decision", tc.decision)
		})
	}

	t.Run("rejects multiple stdin inputs before reading", func(t *testing.T) {
		t.Parallel()
		var stdin guardedReader
		var stdout, stderr strings.Builder
		entry := testkit.Main(decideTool(testWorld("", new(atomic.Int32), nil)).Run)
		code := entry([]string{"read", "--card", "-", "--diff", "-", "--backend", "fixed", "--answers", td + "read-answers.json", "--record", filepath.Join(t.TempDir(), "decisions.jsonl")}, &stdin, &stdout, &stderr)
		assert.Equal(t, 2, code)
		assert.Zero(t, stdin.reads)
		assert.Contains(t, stderr.String(), "--card and --diff all name stdin")
	})
}

type guardedReader struct{ reads int }

func (r *guardedReader) Read([]byte) (int, error) {
	r.reads++
	return 0, errors.New("stdin was read despite conflicting inputs")
}

var _ io.Reader = (*guardedReader)(nil)

func TestDecideExampleDir(t *testing.T) {
	t.Parallel()
	t.Run("dry run plans without creating files", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "planned-example")
		cli.Do(t, "example", "--dir", dir, "--dry-run").Exit(0).Out("EXAMPLE OK", "files=4", "dry_run=true")
		_, err := os.Stat(dir)
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	dir := filepath.Join(t.TempDir(), "decide-example")
	cli.Do(t, "example", "--dir", dir).Exit(0).Out("EXAMPLE OK", "files=4")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 4)
	assert.Equal(t, []string{"answers.json", "sample.diff", "schema.json", "state.txt"}, []string{entries[0].Name(), entries[1].Name(), entries[2].Name(), entries[3].Name()})
	for _, name := range []string{"answers.json", "sample.diff", "schema.json", "state.txt"} {
		assert.FileExists(t, filepath.Join(dir, name))
	}
	assert.NotContains(t, testkit.ReadFile(t, filepath.Join(dir, "schema.json")), "testdata")

	record := filepath.Join(dir, "decisions.jsonl")
	ask := func(op string) {
		cli.Do(t, "ask", "--schema", filepath.Join(dir, "schema.json"), "--state", filepath.Join(dir, "state.txt"),
			"--backend", "fixed", "--answers", filepath.Join(dir, "answers.json"), "--record", record, "--op", op).
			Exit(0).Out("decision=example", "recorded=new")
	}
	ask("example-wrong")
	ask("example-ok")
	cli.Do(t, "outcome", "--record", record, "--id", "example-wrong", "--label", "wrong").Exit(0).Out("label=wrong", "changed=true")
	cli.Do(t, "outcome", "--record", record, "--id", "example-ok", "--label", "ok").Exit(0).Out("label=ok", "changed=true")
	cli.Do(t, "calibrate", "--record", record, "--decision", "example", "--question", "clear", "--positive", "wrong", "--negative", "ok", "--bars", "0.5").
		Exit(0).Out("CALIBRATE OK decision=example", "positives=1 negatives=1 skipped=0")

	marker := filepath.Join(dir, "keep.txt")
	require.NoError(t, os.WriteFile(marker, []byte("keep"), 0o600))
	cli.Do(t, "example", "--dir", dir).Exit(2).Err("already exists")
	assert.Equal(t, "keep", testkit.ReadFile(t, marker))
}
