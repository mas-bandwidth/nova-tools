package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A dry run's note is the plan, not a report of a write: a day that would go
// smaller under --allow-shrink is "would be written smaller", never the past
// tense a run that actually replaced the file uses. The real run keeps the past
// tense, so the two cannot be read as the same event.
func TestFoldDryRunSaysADayWouldBeWrittenSmaller(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	tr := mkdir(t, filepath.Join(dir, "tr"))
	repos := reposFile(t, dir)
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100})+"\n")
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "bench="+tr), 0)

	// The source goes backwards: the same message now reports less than the file.
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 10})+"\n")

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "bench="+tr, "--allow-shrink", "--dry-run")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "dry_run=true")
	note := lineWith(r.stdout, "TOKENS NOTE")
	require.NotEmpty(t, note, "no TOKENS NOTE line:\n%s", r.stdout)
	assert.Contains(t, note, "would be written smaller", "the dry run reported the day as already written:\n%s", note)
	assert.NotContains(t, note, "was written smaller")

	// The real run still says it was written, so a green note is not the plan.
	r = invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "bench="+tr, "--allow-shrink")
	wantExit(t, r, 0)
	assert.Contains(t, lineWith(r.stdout, "TOKENS NOTE"), "was written smaller")
}
