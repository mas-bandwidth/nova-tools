package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testgit"
)

// Unit coverage for wall.go's capture and commit-count seam, untagged and store-free: each
// function's main path and one refusal, over a throwaway git repository in t.TempDir() and
// plain files. WallCommits and wallBaseRef take the repository's own remote refs as their
// input; no Redis, no host.

// coverGit runs one git command in dir and requires success.
func coverGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return coverGitIn(t, os.Environ(), dir, args...)
}

// coverGitIn is coverGit over the environment base, with the test's own git identity
// (internal/testgit) in place of whatever identity the machine has or lacks.
func coverGitIn(t *testing.T, base []string, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = testgit.EnvFrom(base)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// coverInitRepo makes one git repository at dir with one commit on branch.
func coverInitRepo(t *testing.T, dir, branch string) {
	t.Helper()
	coverInitRepoIn(t, os.Environ(), dir, branch)
}

// coverInitRepoIn is coverInitRepo over the environment base.
func coverInitRepoIn(t *testing.T, base []string, dir, branch string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	coverGitIn(t, base, dir, "init", "-b", branch, ".")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte(branch+"\n"), 0o644))
	coverGitIn(t, base, dir, "add", "-A")
	coverGitIn(t, base, dir, "commit", "-q", "-m", "init")
}

// TestWallCoverFenceCaptureReadsBothCaptures: fenceCapture reads harness-output.log first and
// falls back to harness.log, and refuses a job with neither.
func TestWallCoverFenceCaptureReadsBothCaptures(t *testing.T) {
	t.Parallel()

	t.Run("native capture wins", func(t *testing.T) {
		t.Parallel()
		job := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(job, "harness-output.log"), []byte("native\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(job, "harness.log"), []byte("supervisor\n"), 0o644))
		raw, err := fenceCapture(job)
		require.NoError(t, err)
		assert.Equal(t, "native\n", string(raw))
	})
	t.Run("supervisor fallback", func(t *testing.T) {
		t.Parallel()
		job := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(job, "harness.log"), []byte("supervisor\n"), 0o644))
		raw, err := fenceCapture(job)
		require.NoError(t, err)
		assert.Equal(t, "supervisor\n", string(raw))
	})
	t.Run("no capture", func(t *testing.T) {
		t.Parallel()
		job := t.TempDir()
		_, err := fenceCapture(job)
		assert.ErrorContains(t, err, "no harness capture under")
	})
}

// TestWallCoverRepoCommitsCountsPastBase: repoCommits names the branch and counts the commits
// past its base, and reports false for a directory that is not a repository, a detached
// check-out and a branch with nothing past its base.
func TestWallCoverRepoCommitsCountsPastBase(t *testing.T) {
	t.Parallel()

	t.Run("commits past base", func(t *testing.T) {
		t.Parallel()
		job := t.TempDir()
		repo := filepath.Join(job, "repo")
		coverInitRepo(t, repo, "main")
		coverGit(t, repo, "branch", "dev", "HEAD")
		require.NoError(t, os.WriteFile(filepath.Join(repo, "g.txt"), []byte("g\n"), 0o644))
		coverGit(t, repo, "add", "-A")
		coverGit(t, repo, "commit", "-q", "-m", "second")
		coverGit(t, repo, "branch", "--set-upstream-to=dev", "main")
		branch, n, ok := repoCommits(job)
		require.True(t, ok)
		assert.Equal(t, "main", branch)
		assert.Equal(t, 1, n)
	})
	t.Run("nothing past base", func(t *testing.T) {
		t.Parallel()
		job := t.TempDir()
		repo := filepath.Join(job, "repo")
		coverInitRepo(t, repo, "main")
		coverGit(t, repo, "branch", "dev", "HEAD")
		_, _, ok := repoCommits(job)
		assert.False(t, ok, "a branch even with its base counts nothing")
	})
	t.Run("no repository", func(t *testing.T) {
		t.Parallel()
		job := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(job, "repo"), 0o755))
		_, _, ok := repoCommits(job)
		assert.False(t, ok)
	})
	t.Run("detached head", func(t *testing.T) {
		t.Parallel()
		job := t.TempDir()
		repo := filepath.Join(job, "repo")
		coverInitRepo(t, repo, "main")
		coverGit(t, repo, "checkout", "--detach", "HEAD")
		_, _, ok := repoCommits(job)
		assert.False(t, ok)
	})
}

// TestWallCoverRepoBasePicksUpstreamFirst: repoBase prefers the branch's upstream, then
// origin's default-branch spellings, and returns "" when none exists.
func TestWallCoverRepoBasePicksUpstreamFirst(t *testing.T) {
	t.Parallel()

	t.Run("upstream", func(t *testing.T) {
		t.Parallel()
		repo := filepath.Join(t.TempDir(), "repo")
		coverInitRepo(t, repo, "main")
		coverGit(t, repo, "branch", "dev", "HEAD")
		coverGit(t, repo, "branch", "--set-upstream-to=dev", "main")
		assert.Equal(t, "@{upstream}", repoBase(repo))
	})
	t.Run("origin/dev", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		origin := filepath.Join(dir, "origin")
		coverInitRepo(t, origin, "dev")
		repo := filepath.Join(dir, "repo")
		coverInitRepo(t, repo, "work")
		coverGit(t, repo, "remote", "add", "origin", origin)
		coverGit(t, repo, "fetch", "-q", "origin", "refs/heads/*:refs/remotes/origin/*")
		coverGit(t, repo, "update-ref", "-d", "refs/remotes/origin/HEAD")
		assert.Equal(t, "origin/dev", repoBase(repo))
	})
	t.Run("no base", func(t *testing.T) {
		t.Parallel()
		repo := filepath.Join(t.TempDir(), "repo")
		coverInitRepo(t, repo, "main")
		assert.Equal(t, "", repoBase(repo))
	})
}

// TestWallCoverWallCommitsCountsPastBaseRef: WallCommits counts HEAD past the remote-tracking
// base and reports false for an empty directory, a non-repository and a detached check-out.
func TestWallCoverWallCommitsCountsPastBaseRef(t *testing.T) {
	t.Parallel()

	t.Run("commits past origin/dev", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		origin := filepath.Join(dir, "origin")
		coverInitRepo(t, origin, "dev")
		repo := filepath.Join(dir, "repo")
		coverGit(t, dir, "clone", "-q", origin, repo)
		require.NoError(t, os.WriteFile(filepath.Join(repo, "h.txt"), []byte("h\n"), 0o644))
		coverGit(t, repo, "add", "-A")
		coverGit(t, repo, "commit", "-q", "-m", "work")
		branch, n, ok := WallCommits(repo)
		require.True(t, ok)
		assert.Equal(t, "dev", branch)
		assert.Equal(t, 1, n)
	})
	t.Run("empty directory name", func(t *testing.T) {
		t.Parallel()
		_, _, ok := WallCommits("  ")
		assert.False(t, ok)
	})
	t.Run("not a repository", func(t *testing.T) {
		t.Parallel()
		_, _, ok := WallCommits(filepath.Join(t.TempDir(), "repo"))
		assert.False(t, ok)
	})
	t.Run("detached head", func(t *testing.T) {
		t.Parallel()
		repo := filepath.Join(t.TempDir(), "repo")
		coverInitRepo(t, repo, "main")
		coverGit(t, repo, "checkout", "--detach", "HEAD")
		_, _, ok := WallCommits(repo)
		assert.False(t, ok)
	})
}

// TestWallCoverWallCommitsOnARunnerWithNoGitIdentity: the commits-past-origin/dev case
// holds on a machine with no global git identity, the hosted ubuntu-latest runner where
// the clone's `git commit` was refused with `Author identity unknown` (dev CI run
// 37344601638, shard 6): the test's git carries its own identity, never the machine's.
func TestWallCoverWallCommitsOnARunnerWithNoGitIdentity(t *testing.T) {
	t.Parallel()

	runner := testgit.HostedRunnerEnv(os.Environ())
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin")
	coverInitRepoIn(t, runner, origin, "dev")
	repo := filepath.Join(dir, "repo")
	coverGitIn(t, runner, dir, "clone", "-q", origin, repo)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "h.txt"), []byte("h\n"), 0o644))
	coverGitIn(t, runner, repo, "add", "-A")
	coverGitIn(t, runner, repo, "commit", "-q", "-m", "work")
	branch, n, ok := WallCommits(repo)
	require.True(t, ok)
	assert.Equal(t, "dev", branch)
	assert.Equal(t, 1, n)
}

// TestWallCoverWallBaseRefPrefersDefaultBranches: wallBaseRef returns origin/dev, main or
// master first, else the first remote-tracking ref, else "".
func TestWallCoverWallBaseRefPrefersDefaultBranches(t *testing.T) {
	t.Parallel()

	t.Run("dev preferred", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		origin := filepath.Join(dir, "origin")
		coverInitRepo(t, origin, "dev")
		repo := filepath.Join(dir, "repo")
		coverGit(t, dir, "clone", "-q", origin, repo)
		assert.Equal(t, "refs/remotes/origin/dev", wallBaseRef(repo))
	})
	t.Run("first remote ref", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		origin := filepath.Join(dir, "origin")
		coverInitRepo(t, origin, "feat")
		repo := filepath.Join(dir, "repo")
		coverGit(t, dir, "clone", "-q", origin, repo)
		assert.Equal(t, "refs/remotes/origin/feat", wallBaseRef(repo))
	})
	t.Run("no remote refs", func(t *testing.T) {
		t.Parallel()
		repo := filepath.Join(t.TempDir(), "repo")
		coverInitRepo(t, repo, "main")
		assert.Equal(t, "", wallBaseRef(repo))
	})
}

// TestWallCoverGitOutTrimsAndRefuses: gitOut returns the trimmed stdout of a successful git
// and the empty string for a failed one.
func TestWallCoverGitOutTrimsAndRefuses(t *testing.T) {
	t.Parallel()

	repo := filepath.Join(t.TempDir(), "repo")
	coverInitRepo(t, repo, "main")
	assert.Equal(t, "main", gitOut(repo, "rev-parse", "--abbrev-ref", "HEAD"))
	assert.Equal(t, "", gitOut(repo, "rev-parse", "--verify", "no-such-ref"))
}
