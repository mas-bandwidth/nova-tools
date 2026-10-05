package testgit

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyReplacesAnInheritedIdentity(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("git", "status")
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Other", "GIT_AUTHOR_EMAIL=other@example.com",
		"GIT_COMMITTER_NAME=Other", "GIT_COMMITTER_EMAIL=other@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null")
	Apply(cmd)
	got := map[string]string{}
	for _, e := range cmd.Env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		if _, seen := got[k]; seen {
			continue
		}
		got[k] = v
	}
	require.Equal(t, Name, got["GIT_AUTHOR_NAME"])
	require.Equal(t, Email, got["GIT_AUTHOR_EMAIL"])
	require.Equal(t, Name, got["GIT_COMMITTER_NAME"])
	require.Equal(t, Email, got["GIT_COMMITTER_EMAIL"])
	require.Equal(t, "/dev/null", got["GIT_CONFIG_GLOBAL"])
}

func TestApplySetsAuthorAndCommitter(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		Apply(cmd)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	run("-c", "commit.gpgsign=false", "init", "-q", "-b", "main")
	run("-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "fixture")
	cmd := exec.Command("git", "log", "-1", "--format=%an%x00%ae%x00%cn%x00%ce")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	Apply(cmd)
	out, err := cmd.Output()
	require.NoError(t, err, "git log: %v\n%s", err, out)
	parts := strings.Split(strings.TrimSpace(string(out)), "\x00")
	require.Equal(t, []string{Name, Email, Name, Email}, parts)
}
