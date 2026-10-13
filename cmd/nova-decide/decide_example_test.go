package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecideExampleDir pins card decide-example-dir: example --dir writes a
// self-contained starter set that ask, outcome and calibrate run against
// without referencing the repository's testdata.
func TestDecideExampleDir(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "example")
	cli.Do(t, "example", "--dir", dir).Exit(0).Out("EXAMPLE OK")
	for _, name := range []string{"schema.json", "state.txt", "answers.json", "card.md", "card.diff", "read-answers.json"} {
		assert.FileExists(t, filepath.Join(dir, name), name)
	}
	rec := filepath.Join(dir, "decisions.jsonl")
	cli.Do(t, "ask", "--schema", filepath.Join(dir, "schema.json"), "--state", filepath.Join(dir, "state.txt"),
		"--backend", "fixed", "--answers", filepath.Join(dir, "answers.json"), "--record", rec, "--op", "first").Exit(0).Out("ASK OK")
	cli.Do(t, "outcome", "--record", rec, "--id", "first", "--label", "ok").Exit(0).Out("OUTCOME OK")
	// A second labelled decision makes calibrate's positive/negative sets both non-empty.
	cli.Do(t, "ask", "--schema", filepath.Join(dir, "schema.json"), "--state", filepath.Join(dir, "state.txt"),
		"--backend", "fixed", "--answers", filepath.Join(dir, "answers.json"), "--record", rec, "--op", "second").Exit(0)
	cli.Do(t, "outcome", "--record", rec, "--id", "second", "--label", "wrong").Exit(0)
	cli.Do(t, "calibrate", "--record", rec, "--decision", "reply", "--question", "asks_something",
		"--positive", "wrong", "--negative", "ok").Exit(0).Out("CALIBRATE OK")
	raw, err := os.ReadFile(filepath.Join(dir, "schema.json"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"reply"`)
}
