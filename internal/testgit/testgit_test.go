package testgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitIn runs one git command in dir under env and returns its combined output and error.
func gitIn(env []string, dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// scratchRepo is a git repository in a fresh t.TempDir() with one file staged, made
// under env.
func scratchRepo(t *testing.T, env []string) string {
	t.Helper()
	dir := t.TempDir()
	out, err := gitIn(env, dir, "init", "-q", "-b", "main", ".")
	require.NoError(t, err, "git init: %s", out)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("f\n"), 0o644))
	out, err = gitIn(env, dir, "add", "-A")
	require.NoError(t, err, "git add: %s", out)
	return dir
}

// TestHostedRunnerEnvRefusesABareCommit: the simulated hosted runner is the real one's
// failure, a commit with no identity of its own refused with `Author identity unknown`,
// so a test that passes under it does not lean on the machine's git config.
func TestHostedRunnerEnvRefusesABareCommit(t *testing.T) {
	t.Parallel()

	runner := HostedRunnerEnv(os.Environ())
	dir := scratchRepo(t, runner)
	out, err := gitIn(runner, dir, "commit", "-q", "-m", "bare")
	require.Error(t, err, "a commit with no identity succeeded on the simulated runner: %s", out)
	assert.Contains(t, out, "Author identity unknown")
}

// TestEnvCommitsAsTheFixedIdentityOnARunnerWithNone: a commit under EnvFrom succeeds on
// the simulated runner, and its author and committer are Name and Email.
func TestEnvCommitsAsTheFixedIdentityOnARunnerWithNone(t *testing.T) {
	t.Parallel()

	env := EnvFrom(HostedRunnerEnv(os.Environ()))
	dir := scratchRepo(t, env)
	out, err := gitIn(env, dir, "commit", "-q", "-m", "with identity")
	require.NoError(t, err, "git commit: %s", out)
	who, err := gitIn(env, dir, "log", "-1", "--format=%an <%ae>|%cn <%ce>")
	require.NoError(t, err, who)
	want := Name + " <" + Email + ">"
	assert.Equal(t, want+"|"+want, who)
}

// TestEnvFromReplacesTheCallersGitVariables: a GIT_ variable in the caller's environment
// (an identity, a config, a repository) never reaches the test's git; every other
// variable does, and the identity and the shut-out config are there once each.
func TestEnvFromReplacesTheCallersGitVariables(t *testing.T) {
	t.Parallel()

	env := EnvFrom([]string{
		"HOME=/home/x", "PATH=/bin",
		"GIT_AUTHOR_NAME=Hostile Ghost", "GIT_COMMITTER_EMAIL=ghost@example.com",
		"GIT_CONFIG_GLOBAL=/home/x/.gitconfig", "GIT_DIR=/elsewhere/.git",
	})
	assert.Equal(t, []string{
		"HOME=/home/x", "PATH=/bin",
		"GIT_AUTHOR_NAME=" + Name, "GIT_AUTHOR_EMAIL=" + Email,
		"GIT_COMMITTER_NAME=" + Name, "GIT_COMMITTER_EMAIL=" + Email,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
	}, env)
}
