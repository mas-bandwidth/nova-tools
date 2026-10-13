package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecideVerbsAcceptStdinStream pins card decide-stdin-stream-support:
// read, score and gate accept - for their file flags and read stdin.
func TestDecideVerbsAcceptStdinStream(t *testing.T) {
	t.Parallel()
	card, err := os.ReadFile(td + "card.md")
	require.NoError(t, err)
	diff, err := os.ReadFile(td + "card.diff")
	require.NoError(t, err)
	gateOut, err := os.ReadFile(td + "gate-output.txt")
	require.NoError(t, err)

	dir := t.TempDir()

	// read --card -
	rec := filepath.Join(dir, "read.jsonl")
	cli.DoIn(t, string(card), "read", "--card", "-", "--diff", td+"card.diff", "--backend", "fixed", "--answers", td+"read-answers.json", "--record", rec, "--op", "stdin-read").Exit(0).Out("READ OK")
	ds, err := decide.Load(rec)
	require.NoError(t, err)
	require.Len(t, ds, 1)
	assert.Equal(t, "-", ds[0].Inputs["card"])

	// score --diff -
	rec2 := filepath.Join(dir, "score.jsonl")
	cli.DoIn(t, string(diff), "score", "--card", td+"card.md", "--diff", "-", "--backend", "fixed", "--answers", td+"score-answers.json", "--record", rec2, "--op", "stdin-score").Exit(0).Out("SCORE OK")
	ds, err = decide.Load(rec2)
	require.NoError(t, err)
	require.Len(t, ds, 1)
	assert.Equal(t, "-", ds[0].Inputs["diff"])

	// gate --output -
	rec3 := filepath.Join(dir, "gate.jsonl")
	cli.DoIn(t, string(gateOut), "gate", "--output", "-", "--card", td+"card.md", "--diff", td+"card.diff", "--backend", "fixed", "--answers", td+"gate-answers.json", "--record", rec3, "--op", "stdin-gate").Exit(0).Out("GATE OK")
}
