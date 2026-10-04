//go:build functional

package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

// TestAWallDeathNamesItsPathAndKeepsItsCommits: the same auto-reject line, no RESULT.md, and
// one commit in ./repo past its base. The death is a WALL that names the rejected path and
// the commit it kept -- `WALL task=a path=<p> commits=1 branch=<name>` -- so the harvester
// pushes the work instead of the commits being stranded with the card.
func TestAWallDeathNamesItsPathAndKeepsItsCommits(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	reject := "\x1b[33;1m!\x1b[0m  permission requested: external_directory (/outside/scratch/*); auto-rejecting\n"
	require.NoError(t, os.WriteFile(filepath.Join(job, "harness.log"), []byte(reject), 0o644))
	repo := filepath.Join(job, "repo")
	git(t, repo, "init", "-q", "-b", "work")
	git(t, repo, "config", "user.email", "card@example.invalid")
	git(t, repo, "config", "user.name", "card")
	base := commit(t, repo, "base")
	git(t, repo, "update-ref", "refs/remotes/origin/main", base)
	commit(t, repo, "one")

	report, ok := WallDeath(job, "a")
	require.True(t, ok, "a fenced run with no result is a wall death: %s", report)
	for _, want := range []string{"WALL task=a", "path=/outside/scratch/*", "commits=1", "branch=work"} {
		assert.Contains(t, report, want, "the wall report names %q, got %q", want, report)
	}
}

// TestWallCommitsCountsPastTheBase: the branch and the commits a walled card left behind, so
// a harvester can still push them. A clone with no commits past its base says zero.
func TestWallCommitsCountsPastTheBase(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=rowan", "GIT_AUTHOR_EMAIL=rowan@example.com",
			"GIT_COMMITTER_NAME=rowan", "GIT_COMMITTER_EMAIL=rowan@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %v\n%s", args, err, out)
	}
	run("init", "--quiet", "--initial-branch=main")
	run("commit", "--quiet", "--allow-empty", "-m", "base")
	run("update-ref", "refs/remotes/origin/main", "HEAD")
	run("checkout", "--quiet", "-b", "rowan/fix")
	run("commit", "--quiet", "--allow-empty", "-m", "one")
	run("commit", "--quiet", "--allow-empty", "-m", "two")

	branch, commits, ok := WallCommits(repo)
	require.True(t, ok, "WallCommits = %q,%d,%v; want rowan/fix,2,true", branch, commits, ok)
	require.Equal(t, "rowan/fix", branch, "WallCommits = %q,%d,%v; want rowan/fix,2,true", branch, commits, ok)
	require.Equal(t, 2, commits, "WallCommits = %q,%d,%v; want rowan/fix,2,true", branch, commits, ok)
	_, _, ok = WallCommits(filepath.Join(repo, "no-such-dir"))
	assert.False(t, ok, "WallCommits on a directory that is not a clone reports nothing")
}
