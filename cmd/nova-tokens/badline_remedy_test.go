package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A line that is not JSON is not a permission problem. The refusal names the
// file and the line number of the first bad line, and the one TOKENS NOTE
// carries the remedy that fits the act: inspect or remove that line, not
// "open those files to this group".
func TestFoldNamesTheFirstBadLineAndSaysToInspectIt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	repos := reposFile(t, dir)
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100})+"\n{not json\n")

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "bench="+tr)
	wantExit(t, r, 1)
	unreadable := lineWith(r.stderr, "TOKENS UNREADABLE")
	require.NotEmpty(t, unreadable, "no TOKENS UNREADABLE line:\n%s", r.stderr)
	assert.Contains(t, unreadable, "a.jsonl")
	assert.Contains(t, unreadable, "line 2", "the refusal does not name the first bad line:\n%s", unreadable)
	note := lineWith(r.stdout, "TOKENS NOTE")
	require.NotEmpty(t, note, "no TOKENS NOTE line:\n%s", r.stdout)
	assert.Contains(t, note, "a.jsonl", "the remedy does not name the file:\n%s", note)
	assert.Contains(t, note, "line 2", "the remedy does not name the line:\n%s", note)
	assert.Contains(t, note, "inspect or remove", "the remedy does not fit a bad line:\n%s", note)
	wantNotContains(t, note, "open those files to this group")
}
