//go:build functional

package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

// TestStagedCloneIgnoresTheBenchGitconfig is red row
// `staged-clone-ignores-the-bench-gitconfig`: the staged clone carries the
// pool's identity in its LOCAL config and answers it even where the bench's
// own config says otherwise.
func TestStagedCloneIgnoresTheBenchGitconfig(t *testing.T) {
	t.Parallel()

	pool := t.TempDir()
	writePoolIdentity(t, pool, "rowan", "Rowan Friend", "rowan@example.com")
	id, err := LoadPoolIdentity(pool)
	require.NoError(t, err, "a pool with one identity row refuses to load: %v", err)
	require.Equal(t, "Rowan Friend", id.Name, "identity row reads back wrong: %+v", id)
	require.Equal(t, "rowan@example.com", id.Email, "identity row reads back wrong: %+v", id)

	job := t.TempDir()
	repo := filepath.Join(job, "repo")
	initCloneRepo(t, repo)
	err = StageCloneIdentity(repo, id)
	require.NoError(t, err, "staging the clone's identity: %v", err)

	// The bench's own config says somebody else; the clone still answers the pool.
	benchHome := t.TempDir()
	benchConfig := filepath.Join(benchHome, ".gitconfig")
	require.NoError(t, os.WriteFile(benchConfig, []byte("[user]\n\tname = Bench Ghost\n\temail = ghost@example.com\n"), 0o644))
	env := append(os.Environ(),
		"HOME="+benchHome,
		"GIT_CONFIG_GLOBAL="+benchConfig,
		"GIT_CONFIG_NOSYSTEM=0",
	)
	for key, want := range map[string]string{
		"user.name":      "Rowan Friend",
		"user.email":     "rowan@example.com",
		"commit.gpgsign": "false",
		"core.hooksPath": "/dev/null",
	} {
		cmd := exec.Command("git", "config", key)
		cmd.Dir = repo
		cmd.Env = env
		out, err := cmd.Output()
		require.NoError(t, err, "git config %s in the staged clone: %v", key, err)
		got := strings.TrimSpace(string(out))
		assert.Equal(t, want, got, "staged clone %s = %q under the bench's gitconfig, want pool identity %q", key, got, want)
	}

	// The launcher exports the bench config away: the exact two assignments, and
	// exports the pool identity as author and committer.
	env = StagingGitEnv(id)
	joined := strings.Join(env, "\n")
	for _, want := range []string{
		"GIT_AUTHOR_NAME=Rowan Friend",
		"GIT_AUTHOR_EMAIL=rowan@example.com",
		"GIT_COMMITTER_NAME=Rowan Friend",
		"GIT_COMMITTER_EMAIL=rowan@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
	} {
		assert.Contains(t, joined, want, "staging env holds no %q, got %q", want, joined)
	}
}

func initCloneRepo(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %v: %s", args, err, out)
	}
}
