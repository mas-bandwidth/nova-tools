package main

// Issue #181 (coordination: daily retained token collection, cross-bench backfill
// and task-stage joins): a failed attempt followed by success retains both costs.
// SPEC-TOKENS rule 14 says a row for a second attempt (attempt=2) is its own row.
// The swarm reader deduped by job alone, so a retried attempt folded as dup=<n>
// and its tokens never reached the day file.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue181SwarmRetryAttemptIsItsOwnRow(t *testing.T) {
	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	pool := mkdir(t, filepath.Join(dir, "pool"))
	mkdir(t, filepath.Join(pool, "usage"))
	// One job, two attempts: the first failed (rc=1), the retry succeeded (rc=0).
	// Both attempts' spend counts; the retry is not a duplicate.
	failed := strings.Join([]string{
		"j1", "1", "-", "2026-09-11T10:00:00Z", "2026-09-11T10:05:00Z", "done", "1", "deepseek",
		"deepseek-v3", "serialize", "100", "20", "-", "-", "-", "-",
	}, "\t")
	retried := strings.Join([]string{
		"j1", "2", "j1", "2026-09-11T11:00:00Z", "2026-09-11T11:05:00Z", "done", "0", "deepseek",
		"deepseek-v3", "serialize", "1000", "200", "-", "-", "-", "-",
	}, "\t")
	write(t, filepath.Join(pool, "usage", "j1.tsv"),
		strings.Join(swarmHeader, "\t")+"\n"+failed+"\n"+retried+"\n")

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--swarm", "deepseek="+pool)
	wantExit(t, r, 0)
	src := lineWith(r.stdout, "TOKENS SOURCE")
	wantContains(t, src, "dup=0")
	day := read(t, filepath.Join(out, "2026-09-11.tsv"))
	row := lineWith(day, "deepseek-v3\tserialize")
	cols := strings.Split(row, "\t")
	if cols[3] != "1100" || cols[4] != "220" {
		t.Errorf("the failed attempt and its retry did not both fold: %q", row)
	}
}
