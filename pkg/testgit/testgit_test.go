package testgit

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commitIn makes a repository under t.TempDir() and runs one empty commit in it with
// env, returning git's combined output and error.
func commitIn(t *testing.T, env []string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	out, err := exec.Command("git", "init", "-q", repo).CombinedOutput()
	require.NoError(t, err, "git init: %s", out)
	cmd := exec.Command("git", "-C", repo, "commit", "-q", "--allow-empty", "-m", "one")
	cmd.Env = env
	got, err := cmd.CombinedOutput()
	if err != nil {
		return string(got), err
	}
	cmd = exec.Command("git", "-C", repo, "log", "-1", "--format=%an <%ae> %cn <%ce>")
	cmd.Env = env
	got, err = cmd.CombinedOutput()
	return strings.TrimSpace(string(got)), err
}

// TestEnvCommitsWhereTheRunnerHasNoIdentity: with the hosted runner's config (no global
// identity, no guessing) a commit under Environ succeeds as the fixed identity, and the
// same commit without it is refused. The refusal is the CI failure this package exists
// for, reproduced on any machine.
func TestEnvCommitsWhereTheRunnerHasNoIdentity(t *testing.T) {
	t.Parallel()

	t.Run("with the identity", func(t *testing.T) {
		t.Parallel()
		got, err := commitIn(t, Environ(NoGlobalConfig(t)...))
		require.NoError(t, err, "commit under Environ: %s", got)
		assert.Equal(t, Name+" <"+Email+"> "+Name+" <"+Email+">", got)
	})
	t.Run("without it", func(t *testing.T) {
		t.Parallel()
		env := append(exec.Command("git").Environ(), NoGlobalConfig(t)...)
		var kept []string
		for _, kv := range env {
			if !strings.HasPrefix(kv, "GIT_AUTHOR_") && !strings.HasPrefix(kv, "GIT_COMMITTER_") {
				kept = append(kept, kv)
			}
		}
		got, err := commitIn(t, kept)
		require.Error(t, err, "a commit with no identity was accepted: %s", got)
		assert.Contains(t, got, "identity unknown")
	})
}

// TestEnvironPutsTheIdentityAfterTheCaller: a later variable wins in os/exec, so the
// identity follows the caller's environment and extra follows the identity.
func TestEnvironPutsTheIdentityAfterTheCaller(t *testing.T) {
	t.Parallel()
	env := Environ("GIT_AUTHOR_NAME=override")
	require.GreaterOrEqual(t, len(env), 5)
	assert.Equal(t, Env(), env[len(env)-5:len(env)-1])
	assert.Equal(t, "GIT_AUTHOR_NAME=override", env[len(env)-1])
}
