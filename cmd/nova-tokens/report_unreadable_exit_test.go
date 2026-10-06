package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReportThatWroteWithAnUnreadableSourceIsFailed pins the closing word to
// the exit (docs/STANDARD.md section 2). A report that wrote rows and could
// not read a declared source whole says REPORT FAILED, exits 1, and names
// that source. A report that read every source says REPORT OK and exits 0.
// REPORT OK never prints with exit 1.
func TestReportThatWroteWithAnUnreadableSourceIsFailed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	repos := reposFile(t, dir)
	tr := mkdir(t, filepath.Join(dir, "tr"))
	body := msg("m1", "2026-09-11T10:00:00Z", "gemini", map[string]int{"input_tokens": 100}, "/x/schema/a.go") + "\n"
	write(t, filepath.Join(tr, "a.jsonl"), body)
	makeUnreadable(t, write(t, filepath.Join(tr, "b.jsonl"), "{}\n"))

	r := invoke(t, "report", "--who", "ada", "--day", "2026-09-11", "--repos", repos, "--claude", "g="+tr)
	wantExit(t, r, 1)
	wantContains(t, r.stdout, "2026-09-11\tada\tgemini\tschema\tinput\t100")
	unread := lineWith(r.stderr, "TOKENS UNREADABLE")
	require.NotEmpty(t, unread, "stderr:\n%s", r.stderr)
	assert.Contains(t, unread, "label=claude:g")
	failed := lineWith(r.stderr, "REPORT FAILED")
	require.NotEmpty(t, failed, "no REPORT FAILED line:\n%s", r.stderr)
	assert.Contains(t, failed, "source=claude:g", "the failed line does not name the source: %s", failed)
	assert.NotContains(t, r.stderr, "REPORT OK")

	jr, got := asJSON(t, "report", "--who", "ada", "--day", "2026-09-11", "--repos", repos, "--claude", "g="+tr, "--json")
	assert.Equal(t, 1, jr.exit)
	assert.Equal(t, "failed", got.Result.Status)
	assert.Equal(t, 1, got.Result.Exit)

	clean := mkdir(t, filepath.Join(dir, "clean"))
	write(t, filepath.Join(clean, "a.jsonl"), body)
	ok := invoke(t, "report", "--who", "ada", "--day", "2026-09-11", "--repos", repos, "--claude", "g="+clean)
	wantExit(t, ok, 0)
	assert.Contains(t, ok.stderr, "REPORT OK who=ada day=2026-09-11")
	assert.NotContains(t, ok.stderr, "REPORT FAILED")
}
