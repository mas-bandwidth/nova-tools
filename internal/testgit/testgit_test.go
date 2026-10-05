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

// gitIn runs one git command in dir under env and returns its output and error.
func gitIn(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// scratch is a repository with one staged file and no identity of its own.
func scratch(t *testing.T, env []string) string {
	t.Helper()
	dir := t.TempDir()
	out, err := gitIn(dir, env, "init", "-q", ".")
	require.NoError(t, err, "git init: %s", out)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("f\n"), 0o644))
	out, err = gitIn(dir, env, "add", "-A")
	require.NoError(t, err, "git add: %s", out)
	return dir
}

// TestEnvCommitsWithItsOwnIdentityWhereTheMachineHasNone: the witness first -- under the
// isolation alone a commit is refused with git's `Author identity unknown`, as on a hosted
// runner with no global identity, whatever the machine running the test has -- then the
// same commit through Env lands, authored and committed as Name <Email>.
func TestEnvCommitsWithItsOwnIdentityWhereTheMachineHasNone(t *testing.T) {
	t.Parallel()

	bare := isolated(t, os.Environ())
	out, err := gitIn(scratch(t, bare), bare, "commit", "-q", "-m", "bare")
	require.Error(t, err, "a commit with no identity landed; the isolation lets the machine's identity through")
	assert.Contains(t, out, "Author identity unknown")

	env := Env(t, os.Environ())
	dir := scratch(t, env)
	out, err = gitIn(dir, env, "commit", "-q", "-m", "with identity")
	require.NoError(t, err, "git commit: %s", out)
	out, err = gitIn(dir, env, "log", "-1", "--format=%an <%ae>|%cn <%ce>")
	require.NoError(t, err, "git log: %s", out)
	assert.Equal(t, Name+" <"+Email+">|"+Name+" <"+Email+">", out)
}

// TestEnvDropsTheCallersGitVariables: a GIT_ variable of the caller (an identity, a GIT_DIR
// from a hook) never reaches the command; the variables after it are Env's own.
func TestEnvDropsTheCallersGitVariables(t *testing.T) {
	t.Parallel()

	env := Env(t, []string{"PATH=/bin", "GIT_AUTHOR_NAME=Hostile Ghost", "GIT_DIR=/elsewhere"})
	assert.Equal(t, "PATH=/bin", env[0])
	assert.NotContains(t, env, "GIT_AUTHOR_NAME=Hostile Ghost")
	assert.NotContains(t, env, "GIT_DIR=/elsewhere")
	assert.Contains(t, env, "GIT_AUTHOR_NAME="+Name)
	assert.Contains(t, env, "GIT_COMMITTER_EMAIL="+Email)
	assert.Contains(t, env, "GIT_CONFIG_NOSYSTEM=1")
	assert.Equal(t, Env(t, nil), Env(t, nil), "one test's global config is written once and reused")
}
