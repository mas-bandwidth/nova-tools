package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testgit"
	"github.com/stretchr/testify/require"
)

func TestGenerateFromCommitsWritesOneRelandBriefPerCommit(t *testing.T) {
	t.Parallel()
	dir, git := fixtureCheckout(t, map[string]string{
		"base.txt":               "base\n",
		"internal/ci/ci_test.go": "package ci\nimport \"testing\"\nfunc TestCI(t *testing.T) {}\n",
	})
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
		commitFixture(t, dir, "change "+change.file)
		commits = append(commits, git("rev-parse", "HEAD"))
	}
	git("checkout", "-b", "feat")
	mergePath := filepath.Join(dir, "cmd", "m", "m.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(mergePath), 0o755))
	require.NoError(t, os.WriteFile(mergePath, []byte("package m\n"), 0o644))
	git("add", "cmd/m/m.go")
	commitFixture(t, dir, "change cmd/m/m.go")
	git("checkout", "dev")
	git("merge", "--no-ff", "-m", "merge feat", "feat")
	mergeCommit := git("rev-parse", "HEAD")
	out := filepath.Join(t.TempDir(), "cards")
	exit, stdout, stderr := runCard("generate", "--from", "commits", "--range", base+"..HEAD", "--repo-dir", dir, "--out", out)
	require.Zero(t, exit, "stdout: %s\nstderr: %s", stdout, stderr)
	require.Contains(t, stdout, "cards=4 waves=4 tier=pro")
	manifest, err := os.ReadFile(filepath.Join(out, "manifest.tsv"))
	require.NoError(t, err)
	lines := strings.Split(string(manifest), "\n")
	require.Contains(t, lines[1], "land-1\t")
	require.Contains(t, lines[2], "land-2\t")
	require.Contains(t, lines[2], "\t2\tland-1")
	require.Contains(t, lines[3], "land-3\t")
	require.Contains(t, lines[3], "\t3\tland-2")
	require.Contains(t, lines[4], "land-4\t")
	require.Contains(t, lines[4], "\t4\tland-3")
	first, err := os.ReadFile(filepath.Join(out, "land-1.md"))
	require.NoError(t, err)
	second, err := os.ReadFile(filepath.Join(out, "land-2.md"))
	require.NoError(t, err)
	fourth, err := os.ReadFile(filepath.Join(out, "land-4.md"))
	require.NoError(t, err)
	require.Contains(t, string(first), "go test -count=1 -timeout 600s ./cmd/a/ ./internal/ci/")
	require.Contains(t, string(first), "STOP: the exact commit intent is present on this branch, and the STEP 4 gate passes\n")
	require.NotContains(t, string(first), "and the STEP 4 gate passes, and the STEP 4 gate passes")
	require.Contains(t, string(second), "go test -count=1 -timeout 600s ./internal/b/ ./internal/ci/")
	require.Contains(t, string(fourth), "cmd/m/m.go")
	require.Contains(t, string(fourth), "go test -count=1 -timeout 600s ./cmd/m/ ./internal/ci/")
	require.Contains(t, string(fourth), "STOP: the exact commit intent is present on this branch, and the STEP 4 gate passes\n")
	require.NotContains(t, string(fourth), "and the STEP 4 gate passes, and the STEP 4 gate passes")
	require.Contains(t, string(first), "TEST: internal/ci Test")
	exit, stdout, stderr = runCard("generate", "--from", "commits", "--range", base+"..HEAD", "--paths", "cmd/a/*.go", "--repo-dir", dir, "--out", filepath.Join(t.TempDir(), "filtered"), "--dry-run")
	require.Zero(t, exit, "stdout: %s stderr: %s", stdout, stderr)
	require.Contains(t, stdout, "cards=1 waves=1 tier=pro")
	require.NotContains(t, stdout, "internal/b/b.go")
	require.NotContains(t, stdout, "cmd/m/m.go")
	list := filepath.Join(t.TempDir(), "commits.txt")
	require.NoError(t, os.WriteFile(list, []byte(mergeCommit+"\n"+commits[1]+"\n# oldest first is derived, not input order\n"+commits[0]+"\n"), 0o644))
	exit, stdout, stderr = runCard("generate", "--from", "commits", "--file", list, "--paths", "cmd/*", "--repo-dir", dir, "--out", filepath.Join(t.TempDir(), "filtered"), "--dry-run")
	require.Equal(t, 2, exit, "--paths is range-only; stdout: %s stderr: %s", stdout, stderr)
	exit, stdout, stderr = runCard("generate", "--from", "commits", "--file", list, "--repo-dir", dir, "--out", filepath.Join(t.TempDir(), "listed"), "--dry-run")
	require.Zero(t, exit, "stdout: %s stderr: %s", stdout, stderr)
	require.Contains(t, stdout, "land-1\tcmd/a/a.go")
	require.Contains(t, stdout, "land-2\tinternal/b/b.go")
	require.Contains(t, stdout, "land-3\tcmd/m/m.go")
}

func commitFixture(t *testing.T, dir, message string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "commit", "-q", "-m", message)
	cmd.Env = testgit.Environ()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git commit: %s", out)
}
