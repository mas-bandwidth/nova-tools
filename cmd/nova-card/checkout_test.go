package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// fixtureCheckout is a one-commit repository on branch dev with an origin, the
// files written under it; git runs with a clean environment and a fixed identity.
func fixtureCheckout(t *testing.T, files map[string]string) (dir string, git func(args ...string) string) {
	t.Helper()
	dir = t.TempDir()
	git = func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(goenv.Clean(os.Environ()), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
		return strings.TrimSpace(string(out))
	}
	for rel, text := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(text), 0o644))
	}
	git("init", "-q", "-b", "dev")
	git("remote", "add", "origin", "git@example.com:example/repo.git")
	git("add", ".")
	git("commit", "-q", "-m", "fixture")
	return dir, git
}

// readCheckout reads the branch off a checkout's HEAD, and a detached HEAD names
// none, so generate refuses rather than writing BASE: HEAD into every card. The
// branch is read by git symbolic-ref, never by comparing a name to HEAD
// (internal/typedrec TestOneTypedParser holds the tree to that).
func TestReadCheckoutReadsTheBranchAndNoneAtADetachedHEAD(t *testing.T) {
	t.Parallel()
	dir, git := fixtureCheckout(t, map[string]string{"a.txt": "a\n"})
	var h cardgen.Header
	require.NoError(t, readCheckout(dir, &h))
	assert.Equal(t, "dev", h.Base)
	assert.Equal(t, "example/repo", h.Repo)
	assert.Regexp(t, "^[0-9a-f]{40}$", h.Sha)

	git("checkout", "-q", "--detach")
	detached := cardgen.Header{}
	require.NoError(t, readCheckout(dir, &detached))
	assert.Empty(t, detached.Base, "a detached HEAD is no branch")
	assert.Equal(t, h.Sha, detached.Sha)
	exit, _, stderr := runCard("generate", "--from", "findings", "--file", "testdata/findings.tsv", "--repo-dir", dir, "--out", filepath.Join(t.TempDir(), "cards"))
	assert.Equal(t, 2, exit)
	assert.Contains(t, stderr, "no base branch")
}

// A directory cut by --max is admitted as it is: no kept card needs a cut one.
func TestMaxLeavesNoNeedOnACutCard(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	ledger := cardgen.Ledgers["serial-tests"]
	for rel, text := range map[string]string{
		"cmd/a/a_test.go": "package main\n", "cmd/b/b_test.go": "package main\n", "cmd/c/c_test.go": "package main\n",
		ledger.File: "cmd/a/a_test.go:TestA serial: t.Setenv\ncmd/b/b_test.go:TestB serial: t.Chdir\ncmd/c/c_test.go:TestC serial: os.Setenv\n",
	} {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(text), 0o644))
	}
	exit, stdout, stderr := runCard("generate", "--from", "ledger", "--ledger", "serial-tests", "--repo-dir", repo, "--repo", "o/r", "--base", "dev", "--sha", strings.Repeat("ab", 20), "--out", filepath.Join(t.TempDir(), "cards"), "--max", "2", "--dry-run")
	require.Equal(t, 0, exit, stderr)
	assert.Contains(t, stdout, "cards=2 waves=2 tier=flash dry-run=yes")
	assert.Contains(t, stdout, "serial-tests-cmd-b-b\tcmd/b/b_test.go\tinternal/ci TestEveryTestOpensWithTParallel\t2\tserial-tests-cmd-a-a\n")
	assert.NotContains(t, stdout, "serial-tests-cmd-c-c", "the cut card is named nowhere")
}
