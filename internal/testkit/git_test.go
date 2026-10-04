package testkit_test

import (
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

func TestGitCommitWritesTheFilesAndLeavesTheTreeClean(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	clone := g.Clones[0]
	g.Commit(clone, map[string]string{"src/a.txt": "a\n", "b.txt": "b\n"})
	assert.Equal(t, "a\n", testkit.ReadFile(t, filepath.Join(clone, "src", "a.txt")))
	assert.Empty(t, strings.TrimSpace(g.Run(clone, "status", "--porcelain")), "the commit left the work tree dirty")
}

func TestGitCommitUsesTheFixedIdentityNotTheMachine(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	clone := g.Clones[0]
	g.Commit(clone, map[string]string{"a.txt": "a\n"})
	out := g.Run(clone, "log", "--format=%an <%ae>", "-1")
	assert.Equal(t, "testkit <testkit@example.com>", strings.TrimSpace(out), "the commit's identity is the rig's fixed one")
}

func TestGitPushAndHeadFollowTheCommitToTheRemote(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 2)
	clone := g.Clones[0]
	g.Commit(clone, map[string]string{"a.txt": "a\n"})
	g.Push(clone)
	assert.Equal(t, g.Head(clone), g.Head(g.Remote), "the remote's HEAD is the pushed commit")
}

func TestGitFailsTheTestWhenGitFails(t *testing.T) {
	t.Parallel()
	rec := &recorder{TB: t}
	g := testkit.Git(rec, 1)
	runs(rec, func() { g.Head(g.Clones[0]) })
	assert.True(t, rec.failed, "Head passed a repository with no commits")
}
