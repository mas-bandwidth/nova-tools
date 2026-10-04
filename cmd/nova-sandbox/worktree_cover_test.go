package main

// The cover tests for worktree.go's two seams the existing suite never reaches:
// runGit (the production git runner) and inUseByAProcess (the default process
// probe --prune's stale rule reads). runGit is exercised through the real git
// on PATH on a throwaway repository this test makes in its own t.TempDir(); no
// subprocess is faked because the function IS the subprocess seam, and git on a
// local repository is no network and no host. inUseByAProcess is exercised with
// this test process's own working directory (a certain "in use") and a directory
// no process holds (a certain "not in use"), so both sides of its rule run.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// needGitOnPath skips a test that calls the real runGit where there is no git
// on PATH, so the cover test is a skip and never a false red.
func needGitOnPath(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("no git on PATH: %v", err)
	}
}

// initRepo makes a real, empty git repository in a fresh directory and returns
// it resolved through symlinks (git reports a work tree at its resolved path,
// and t.TempDir on macOS is under /var -> /private/var).
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	out, err := runGit(dir, "init", "--quiet")
	require.NoError(t, err, "git init in %s: %s", dir, out)
	return dir
}

// TestWorktreeCoverRunGitReportsSuccessAndFailure pins both arms of runGit: a
// real git that answers returns its stdout with no error, and a real git that
// refuses folds its own stderr into the error so a caller has a breadcrumb.
func TestWorktreeCoverRunGitReportsSuccessAndFailure(t *testing.T) {
	t.Parallel()
	needGitOnPath(t)
	repo := initRepo(t)

	out, err := runGit(repo, "rev-parse", "--is-inside-work-tree")
	require.NoError(t, err, "rev-parse in a real work tree: %s", out)
	assert.Equal(t, "true", strings.TrimSpace(out), "rev-parse stdout %q, want true", out)

	out, err = runGit(repo, "not-a-git-subcommand")
	require.Error(t, err, "git accepted %q and returned %q", "not-a-git-subcommand", out)
	assert.Contains(t, err.Error(), "git not-a-git-subcommand:", "the failure is not named: %v", err)
	assert.Contains(t, err.Error(), "not a git command", "git's own stderr is not folded in: %v", err)
}

// TestWorktreeCoverRunGitNamesTheDirectory pins that the dir runGit is handed
// reaches git's -C: a command run in a real repository answers for THAT
// repository, and the same command run in a directory that is not one fails,
// so the directory is what decided the answer.
func TestWorktreeCoverRunGitNamesTheDirectory(t *testing.T) {
	t.Parallel()
	needGitOnPath(t)
	repo := initRepo(t)

	inRepo, err := runGit(repo, "rev-parse", "--is-inside-work-tree")
	require.NoError(t, err, "rev-parse in %s: %s", repo, inRepo)

	other := t.TempDir()
	out, err := runGit(other, "rev-parse", "--is-inside-work-tree")
	require.Error(t, err, "a directory outside a repository answered %q", out)
	assert.Contains(t, err.Error(), "not a git repository", "the failure does not say why: %v", err)
}

// TestWorktreeCoverInUseByAProcessAnswersBothSides pins both sides of the
// probe: this test process's own resolved working directory is held by a
// process and answers true, and a directory no live process holds answers
// false. Where the probe cannot read the process table (a platform other than
// linux) it says yes, and the test asserts that instead.
func TestWorktreeCoverInUseByAProcessAnswersBothSides(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.True(t, inUseByAProcess(cwd), "the probe does not see this test process holding its own working directory %s", cwd)

	free := t.TempDir()
	if r, err := filepath.EvalSymlinks(free); err == nil {
		free = r
	}
	got := inUseByAProcess(free)
	if runtime.GOOS == "linux" {
		assert.False(t, got, "a fresh empty directory %s no process holds is reported in use", free)
	} else {
		assert.True(t, got, "on %s the probe cannot read the process table and must answer yes, got %v for %s", runtime.GOOS, got, free)
	}
}
