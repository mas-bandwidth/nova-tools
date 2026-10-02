package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// treeOf is every entry under root: its relative path, its kind and mode, and a file's
// content hash. Two snapshots are equal only when nothing under root was made, removed,
// truncated or rewritten.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		entry := info.Mode().String()
		if info.Mode().IsRegular() {
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(raw)
			entry += " " + hex.EncodeToString(sum[:])
		}
		tree[filepath.ToSlash(rel)] = entry
		return nil
	}))
	return tree
}

// Every writing verb's --dry-run over a fixture tree leaves the whole tree as it was:
// the sources, the output directory, the note's directory and a session's --out parent.
func TestEveryDryRunLeavesTheWholeTreeAsItWas(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bench := filepath.Join(root, "bench")
	copyExampleTree(t, filepath.Join("testdata", "example-bench"), bench)
	out := mkdir(t, filepath.Join(root, "out"))
	repos, tr := filepath.Join(bench, "repos.tsv"), filepath.Join(bench, "transcripts")
	// A folded day already there, so a dry run has a file it could have replaced.
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "bench="+tr), 0)
	session := write(t, filepath.Join(root, "session.jsonl"), msg("s1", "2026-09-11T10:00:00Z", "claude-opus-5", map[string]int{"input_tokens": 3})+"\n")
	note := write(t, filepath.Join(root, "notes", "note.txt"), "an earlier note\n")

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"fold", []string{"fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "bench=" + tr, "--allow-shrink"}},
		{"fold --all", []string{"fold", "--out", out, "--all", "--repos", repos, "--claude", "bench=" + tr}},
		{"report --note", []string{"report", "--who", "ada", "--day", "2026-09-11", "--repos", repos, "--claude", "bench=" + tr, "--note", note}},
		{"session --out, existing", []string{"session", "--claude-session", session, "--out", out}},
		{"session --out, new", []string{"session", "--claude-session", session, "--out", filepath.Join(root, "new", "out")}},
		{"ledger", []string{"ledger", "--out", out, "--day", "2026-09-11", "--redis", "127.0.0.1:0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := treeOf(t, root)
			r := invoke(t, append(append([]string(nil), tc.args...), "--dry-run")...)
			wantExit(t, r, 0)
			assert.Contains(t, r.all(), "dry_run=true")
			assert.Equal(t, before, treeOf(t, root), "--dry-run changed the tree")
		})
	}
}
