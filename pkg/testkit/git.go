package testkit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/stretchr/testify/require"
)

// GitRig is a bare git remote and clones of it, all under the test's own
// t.TempDir(), for the tests that must run real git (docs/STANDARD.md, section
// 8: shared rigs live in pkg/testkit, never copies; section 9 rule 10: a
// test writes only inside its own t.TempDir()). Every command runs with a
// fixed identity and GIT_CONFIG_GLOBAL pointed at an empty file inside the
// rig's directory, through the command's environment, never t.Setenv, so the
// machine's own git identity and config cannot reach the test.
type GitRig struct {
	t   testing.TB
	git string
	env []string
	// Remote is the bare remote's path; Clones are the clones', in the order made.
	Remote string
	Clones []string
}

// Git builds a rig of one bare remote and n clones of it, failing the test when
// git is not on PATH or a git command fails. It runs the git exec.LookPath
// finds on PATH and no other.
func Git(t testing.TB, n int) *GitRig {
	t.Helper()
	git, err := exec.LookPath("git")
	require.NoError(t, err, "the git rig runs the git on PATH")
	dir := t.TempDir()
	global := filepath.Join(dir, "gitconfig")
	WriteFile(t, global, "")
	g := &GitRig{t: t, git: git, env: gitEnv(global), Remote: filepath.Join(dir, "remote")}
	g.run(dir, "init", "--bare", g.Remote)
	for i := range n {
		clone := filepath.Join(dir, "clone"+strconv.Itoa(i))
		g.run(dir, "clone", g.Remote, clone)
		g.Clones = append(g.Clones, clone)
	}
	return g
}

// gitEnv is the caller's environment without its GIT_ variables, then the
// rig's fixed identity, the system config off and the global config at the
// rig's empty file, so the command's git settings are the rig's alone.
func gitEnv(global string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	return append(env,
		"GIT_AUTHOR_NAME=testkit", "GIT_AUTHOR_EMAIL=testkit@example.com",
		"GIT_COMMITTER_NAME=testkit", "GIT_COMMITTER_EMAIL=testkit@example.com",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+global)
}

// Commit writes files (a path under the clone to its body) into the clone and
// commits them under the rig's fixed identity.
func (g *GitRig) Commit(clone string, files map[string]string) {
	g.t.Helper()
	for name, body := range files {
		WriteFile(g.t, filepath.Join(clone, name), body)
	}
	g.run(clone, "add", "-A")
	g.run(clone, "commit", "-q", "-m", "testkit commit")
}

// Push is one git push of the clone's current branch from the clone to the
// rig's remote, so the remote's branch follows the clone's.
func (g *GitRig) Push(clone string) {
	g.t.Helper()
	g.run(clone, "push", "-q", g.Remote, "HEAD")
}

// Head returns the commit HEAD names in repo, the remote's or a clone's.
func (g *GitRig) Head(repo string) string {
	g.t.Helper()
	return strings.TrimSpace(g.run(repo, "rev-parse", "HEAD"))
}

// run runs one git command in dir under the rig's environment through
// pkg/gitrun, the tree's one door to git (bounded by its deadline),
// failing the test with git's stderr on any error, and returns its stdout.
func (g *GitRig) run(dir string, args ...string) string {
	g.t.Helper()
	res, err := gitrun.Run(context.Background(), gitrun.Options{Bin: g.git, Dir: dir, Env: g.env}, args...)
	require.NoError(g.t, err, "git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(res.Stderr)))
	return string(res.Stdout)
}
