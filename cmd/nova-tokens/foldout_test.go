package main

// The fold's --out handling: an absent output directory is refused (exit 2), and an
// existing path that is not a directory is refused by name and never overwritten (#1502).

import (
	"github.com/stretchr/testify/assert"
	"path/filepath"
	"testing"
)

func TestFoldOutAbsent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tr := mkdir(t, filepath.Join(dir, "tr"))
	repos := reposFile(t, dir)
	write(t, filepath.Join(tr, "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100, "output_tokens": 10}, "/x/schema/a.go")+"\n")

	t.Run("an absent out directory is refused and not created", func(t *testing.T) {
		out := filepath.Join(dir, "out")
		r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "seat-a="+tr)
		wantExit(t, r, 2)
		wantContains(t, r.stderr, "does not exist")
		wantContains(t, r.stderr, out)
	})

	t.Run("a regular file at the out path is refused and untouched", func(t *testing.T) {
		out := write(t, filepath.Join(dir, "outfile"), "a regular file\n")
		r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "seat-a="+tr)
		wantExit(t, r, 2)
		wantContains(t, r.stderr, "is not a directory")
		wantContains(t, r.stderr, out)
		{
			got := read(t, out)
			assert.Equal(t, "a regular file\n", got, "the file at --out was overwritten: %q", got)
		}
	})
}
