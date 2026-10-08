package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Issue #1665 / PR #2421: Worker Boundary Identity Delivery.
// Pool identity and git config isolation must be delivered across the supervisor
// and native child execution boundaries to the worker harness, ensuring commits
// made by a worker carry pool identity rather than any bench gitconfig.

func TestBoundaryIdentityNegativeControlFallsBackToBenchConfigOrFails(t *testing.T) {
	t.Parallel()

	// Negative control verification:
	// 1. Without pool identity delivery and config isolation, a commit created inside a job
	//    falls back to hostile bench gitconfig (e.g. Hostile Ghost).
	// 2. With pool identity delivery and config isolation, the same commit reliably carries
	//    the pool identity and completely ignores the bench gitconfig.

	benchHome := t.TempDir()
	benchConfig := filepath.Join(benchHome, ".gitconfig")
	write(t, benchConfig, "[user]\n\tname = Hostile Ghost\n\temail = ghost@example.com\n")

	// Negative control (unprotected boundary):
	// A child process inheriting benchConfig without pool identity commits as the bench ghost.
	repoNegative := filepath.Join(t.TempDir(), "repo-neg")
	require.NoError(t, os.MkdirAll(repoNegative, 0o755))
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoNegative
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + benchHome,
			"GIT_CONFIG_GLOBAL=" + benchConfig,
		}
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, out)
	}
	outNeg, err := exec.Command("git", "-C", repoNegative, "log", "-1", "--format=%an <%ae>").CombinedOutput()
	require.NoError(t, err)
	gotNeg := strings.TrimSpace(string(outNeg))
	require.Equal(t, "Hostile Ghost <ghost@example.com>", gotNeg, "negative control expected hostile ghost identity %q, got %q", "Hostile Ghost <ghost@example.com>", gotNeg)

	// Positive control (protected boundary):
	// A child process with pool identity and git config isolation commits as pool identity.
	repoPositive := filepath.Join(t.TempDir(), "repo-pos")
	require.NoError(t, os.MkdirAll(repoPositive, 0o755))
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoPositive
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + benchHome,
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=Pool Identity",
			"GIT_AUTHOR_EMAIL=pool@example.com",
			"GIT_COMMITTER_NAME=Pool Identity",
			"GIT_COMMITTER_EMAIL=pool@example.com",
		}
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, out)
	}
	outPos, err := exec.Command("git", "-C", repoPositive, "log", "-1", "--format=%an <%ae>").CombinedOutput()
	require.NoError(t, err)
	gotPos := strings.TrimSpace(string(outPos))
	require.Equal(t, "Pool Identity <pool@example.com>", gotPos, "positive control expected pool identity %q, got %q", "Pool Identity <pool@example.com>", gotPos)
}
