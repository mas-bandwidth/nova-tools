package main

// The --max-files ceiling and the --exclude glob over a --claude tree: the tool counts the
// tree before it opens a file, so a tree over the ceiling fails the run naming the files
// and bytes it found, and --exclude is the remedy that keeps a temporary tree out.

import (
	"path/filepath"
	"testing"
)

// TestFoldRefusesATreeOverMaxFilesBeforeReadingIt: a --claude tree holding two transcript
// files under --max-files 1 is refused with the totals it found and the run says FAILED
// without writing a day; adding --exclude for the temporary tree admits the same ceiling.
func TestFoldRefusesATreeOverMaxFilesBeforeReadingIt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tr := mkdir(t, filepath.Join(dir, "transcripts"))
	repos := reposFile(t, dir)
	write(t, filepath.Join(tr, "good.jsonl"),
		msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 10}, "/x/schema/a.go")+"\n")
	write(t, filepath.Join(tr, "tmp", "scratch.jsonl"),
		msg("m2", "2026-09-11T11:00:00Z", "fable", map[string]int{"input_tokens": 20}, "/x/schema/b.go")+"\n")

	blocked := mkdir(t, filepath.Join(dir, "blocked"))
	r := invoke(t, "fold", "--out", blocked, "--day", "2026-09-11", "--repos", repos,
		"--claude", "bench="+tr, "--max-files", "1")
	wantExit(t, r, 1)
	wantContains(t, r.stderr, "2 transcript files")
	wantContains(t, r.stderr, "over the --max-files ceiling of 1")
	wantContains(t, r.stderr, "--exclude")
	wantContains(t, r.stdout, "TOKENS FAILED")

	// The temporary tree is what put the source over the ceiling, and --exclude is the
	// remedy the refusal names: the same call admits the real transcript.
	clean := mkdir(t, filepath.Join(dir, "clean"))
	r = invoke(t, "fold", "--out", clean, "--day", "2026-09-11", "--repos", repos,
		"--claude", "bench="+tr, "--max-files", "1", "--exclude", "tmp")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "TOKENS OK")
}

// TestMaxFilesDefaultIsStatedInFoldHelp: the generated help names the flag and its default,
// because verbflag prints no default on its own.
func TestMaxFilesDefaultIsStatedInFoldHelp(t *testing.T) {
	t.Parallel()

	r := invoke(t, "fold", "-h")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "--max-files")
	wantContains(t, r.stdout, "default 20000")
	wantContains(t, r.stdout, "--exclude")
}
