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

// A dry run is the real run's own plan: the same validation and the same refusals, with
// only the final write skipped. Each row is an invocation the real run refuses or fails;
// with --dry-run it must answer the same, line for line.
func TestADryRunRefusesExactlyWhatTheRealRunRefuses(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bench := filepath.Join(root, "bench")
	copyExampleTree(t, filepath.Join("testdata", "example-bench"), bench)
	out := mkdir(t, filepath.Join(root, "out"))
	repos, tr := filepath.Join(bench, "repos.tsv"), filepath.Join(bench, "transcripts")
	aFile := write(t, filepath.Join(root, "a-file"), "not a directory\n")
	session := write(t, filepath.Join(root, "session.jsonl"), msg("s1", "2026-09-11T10:00:00Z", "claude-opus-5", map[string]int{"input_tokens": 3})+"\n")
	wantExit(t, invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "bench="+tr), 0)
	report := []string{"report", "--who", "ada", "--day", "2026-09-11", "--repos", repos, "--claude", "bench=" + tr}

	for _, tc := range []struct {
		name string
		args []string
		exit int
	}{
		{"fold with no flags", []string{"fold"}, 2},
		{"fold into a file", []string{"fold", "--out", aFile, "--day", "2026-09-11", "--repos", repos, "--claude", "bench=" + tr}, 2},
		{"fold with no rules file", []string{"fold", "--out", out, "--day", "2026-09-11", "--repos", filepath.Join(root, "none.tsv"), "--claude", "bench=" + tr}, 2},
		{"fold with --scratch and no --opencode", []string{"fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "bench=" + tr, "--scratch", root}, 2},
		{"report with no --who", []string{"report", "--day", "2026-09-11", "--repos", repos, "--claude", "bench=" + tr}, 2},
		{"report --note into a missing directory", append(append([]string(nil), report...), "--note", filepath.Join(root, "missing", "note.txt")), 2},
		{"report --note onto a directory", append(append([]string(nil), report...), "--note", out), 2},
		{"ledger with no --redis", []string{"ledger", "--out", out, "--day", "2026-09-11"}, 2},
		{"ledger whose user has no password", []string{"ledger", "--out", out, "--day", "2026-09-11", "--redis", "127.0.0.1:0",
			"--user", "bench", "--password-env", "NOVA_TOKENS_TEST_UNSET_PASSWORD_VARIABLE"}, 1},
		{"session with no transcript", []string{"session", "--out", out}, 2},
		{"session --out a file", []string{"session", "--claude-session", session, "--out", aFile}, 2},
		{"session --out below a file", []string{"session", "--claude-session", session, "--out", filepath.Join(aFile, "out")}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			real := invoke(t, tc.args...)
			dry := invoke(t, append(append([]string(nil), tc.args...), "--dry-run")...)
			assert.Equal(t, tc.exit, real.exit, "the real run: %s", real.all())
			assert.Equal(t, real.exit, dry.exit, "--dry-run answered %s", dry.all())
			assert.Equal(t, real.stderr, dry.stderr)
		})
	}
}
