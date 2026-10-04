package testkit_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitBuildsABareRemoteAndTheClonesAsked(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 2)
	assert.DirExists(t, filepath.Join(g.Remote, "objects"), "a bare repository keeps its objects at the top")
	require.Len(t, g.Clones, 2)
	assert.DirExists(t, filepath.Join(g.Clones[1], ".git", "objects"), "a clone is a work tree holding its repository in .git")
}

func TestGitCommitWritesTheFilesAndMovesHead(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	clone := g.Clones[0]
	g.Commit(clone, map[string]string{"src/a.txt": "a\n", "b.txt": "b\n"})
	assert.Equal(t, "a\n", testkit.ReadFile(t, filepath.Join(clone, "src", "a.txt")))
	assert.NotEmpty(t, g.Head(clone), "the commit left HEAD unborn")
}

func TestGitCommitUsesTheFixedIdentityNotTheMachine(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	clone := g.Clones[0]
	g.Commit(clone, map[string]string{"a.txt": "a\n"})
	out, err := exec.Command("git", "-C", clone, "log", "-1", "--format=%an <%ae>").Output()
	require.NoError(t, err)
	assert.Equal(t, "testkit <testkit@example.com>", strings.TrimSpace(string(out)), "the commit's identity is the rig's fixed one, not the machine's")
}

func TestGitPushFollowsTheCommitToTheRemote(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 2)
	clone := g.Clones[0]
	g.Commit(clone, map[string]string{"a.txt": "a\n"})
	g.Push(clone)
	assert.Equal(t, g.Head(clone), g.Head(g.Remote), "the remote's head is the pushed commit")
}

func TestGitHeadFailsTheTestOnARepositoryWithNoCommit(t *testing.T) {
	t.Parallel()
	rec := &recorder{TB: t}
	g := testkit.Git(rec, 1)
	runs(rec, func() { g.Head(g.Clones[0]) })
	assert.True(t, rec.failed, "Head passed a repository with no commit")
}
