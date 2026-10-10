package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateFromCommitsWritesOneRelandBriefPerCommit(t *testing.T) {
	t.Parallel()
	dir, git := fixtureCheckout(t, map[string]string{"base.txt": "base\n"})
	base := git("rev-parse", "HEAD")
	var commits []string
	for _, change := range []struct{ file, body string }{
		{"cmd/a/a.go", "package a\n"},
		{"internal/b/b.go", "package b\n"},
	} {
		path := filepath.Join(dir, filepath.FromSlash(change.file))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(change.body), 0o644))
		git("add", change.file)
		git("commit", "-q", "-m", "change "+change.file)
		commits = append(commits, git("rev-parse", "HEAD"))
	}
	out := filepath.Join(t.TempDir(), "cards")
	exit, stdout, stderr := runCard("generate", "--from", "commits", "--range", base+"..HEAD", "--repo-dir", dir, "--out", out)
	require.Zero(t, exit, "stdout: %s\nstderr: %s", stdout, stderr)
	require.Contains(t, stdout, "cards=2 waves=2 tier=pro")
	lines := strings.Split(stdout, "\n")
	require.Contains(t, lines[1], "land-1\t")
	require.Contains(t, lines[2], "land-2\t")
	require.Contains(t, lines[2], "\t1\tland-1")
	first, err := os.ReadFile(filepath.Join(out, "land-1.md"))
	require.NoError(t, err)
	second, err := os.ReadFile(filepath.Join(out, "land-2.md"))
	require.NoError(t, err)
	require.Contains(t, string(first), "go test -count=1 -timeout 600s ./cmd/a/ ./internal/ci/")
	require.Contains(t, string(second), "go test -count=1 -timeout 600s ./internal/b/ ./internal/ci/")
	require.Contains(t, string(first), "TEST: none (re-land commit)")
	require.NotContains(t, string(first), "TEST: ./internal/ci TestX")
	exit, stdout, stderr = runCard("generate", "--from", "commits", "--range", base+"..HEAD", "--paths", "cmd/a/*.go", "--repo-dir", dir, "--out", filepath.Join(t.TempDir(), "filtered"), "--dry-run")
	require.Zero(t, exit, "stdout: %s stderr: %s", stdout, stderr)
	require.Contains(t, stdout, "cards=1 waves=1 tier=pro")
	require.NotContains(t, stdout, "internal/b/b.go")
	list := filepath.Join(t.TempDir(), "commits.txt")
	require.NoError(t, os.WriteFile(list, []byte(commits[1]+"\n# oldest first is derived, not input order\n"+commits[0]+"\n"), 0o644))
	exit, stdout, stderr = runCard("generate", "--from", "commits", "--file", list, "--paths", "cmd/*", "--repo-dir", dir, "--out", filepath.Join(t.TempDir(), "filtered"), "--dry-run")
	require.Equal(t, 2, exit, "--paths is range-only; stdout: %s stderr: %s", stdout, stderr)
	exit, stdout, stderr = runCard("generate", "--from", "commits", "--file", list, "--repo-dir", dir, "--out", filepath.Join(t.TempDir(), "listed"), "--dry-run")
	require.Zero(t, exit, "stdout: %s stderr: %s", stdout, stderr)
	require.Contains(t, stdout, "land-1\tcmd/a/a.go")
	require.Contains(t, stdout, "land-2\tinternal/b/b.go")
}
