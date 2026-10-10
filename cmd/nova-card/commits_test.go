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
	out := filepath.Join(t.TempDir(), "cards")
	exit, stdout, stderr := runCard("generate", "--from", "commits", "--range", base+"..HEAD", "--repo-dir", dir, "--out", out)
	require.Zero(t, exit, "stdout: %s\nstderr: %s", stdout, stderr)
	require.Contains(t, stdout, "cards=2 waves=2 tier=pro")
	manifest, err := os.ReadFile(filepath.Join(out, "manifest.tsv"))
	require.NoError(t, err)
	lines := strings.Split(string(manifest), "\n")
	require.Contains(t, lines[1], "land-1\t")
	require.Contains(t, lines[2], "land-2\t")
	require.Contains(t, lines[2], "\t2\tland-1")
	first, err := os.ReadFile(filepath.Join(out, "land-1.md"))
	require.NoError(t, err)
	second, err := os.ReadFile(filepath.Join(out, "land-2.md"))
	require.NoError(t, err)
	require.Contains(t, string(first), "go test -count=1 -timeout 600s ./cmd/a/ ./internal/ci/")
	require.Contains(t, string(second), "go test -count=1 -timeout 600s ./internal/b/ ./internal/ci/")
	require.Contains(t, string(first), "TEST: internal/ci Test")
	require.Contains(t, string(first), "STOP: the exact commit intent is present on this branch, and the STEP 4 gate passes")
	require.NotContains(t, string(first), "gate passes, and the STEP 4 gate passes", "the gate clause is appended once")
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

	// A merge commit is the exact input re-land exists for: the tree's landed
	// commits are merges, where `land <stream> (<card>)` has two parents. git
	// diff-tree prints nothing for a merge without -m, so commitPaths asks for the
	// first-parent diff; without it every merge has no paths and no brief.
	mergeDir, mgit := fixtureCheckout(t, map[string]string{
		"base.txt":               "base\n",
		"internal/ci/ci_test.go": "package ci\nimport \"testing\"\nfunc TestCI(t *testing.T) {}\n",
	})
	mergeBase := mgit("rev-parse", "HEAD")
	mgit("checkout", "-q", "-b", "side")
	sidePath := filepath.Join(mergeDir, "internal", "side", "side.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(sidePath), 0o755))
	require.NoError(t, os.WriteFile(sidePath, []byte("package side\n"), 0o644))
	mgit("add", "internal/side/side.go")
	commitFixture(t, mergeDir, "side adds internal/side/side.go")
	mgit("checkout", "-q", "dev")
	mgit("merge", "-q", "--no-ff", "--no-commit", "side")
	mergeOnly := filepath.Join(mergeDir, "cmd", "mergeonly", "mergeonly.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(mergeOnly), 0o755))
	require.NoError(t, os.WriteFile(mergeOnly, []byte("package mergeonly\n"), 0o644))
	mgit("add", "cmd/mergeonly/mergeonly.go")
	commitFixture(t, mergeDir, "merge side adds cmd/mergeonly/mergeonly.go")
	mergeOut := filepath.Join(t.TempDir(), "merged")
	mergeExit, mergeStdout, mergeStderr := runCard("generate", "--from", "commits", "--range", mergeBase+"..HEAD", "--repo-dir", mergeDir, "--out", mergeOut)
	require.Zero(t, mergeExit, "stdout: %s\nstderr: %s", mergeStdout, mergeStderr)
	mergeManifest, err := os.ReadFile(filepath.Join(mergeOut, "manifest.tsv"))
	require.NoError(t, err)
	require.Contains(t, string(mergeManifest), "cmd/mergeonly/mergeonly.go", "the merge's first-parent files become a brief")
}

func commitFixture(t *testing.T, dir, message string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "commit", "-q", "-m", message)
	cmd.Env = testgit.Environ()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git commit: %s", out)
}
