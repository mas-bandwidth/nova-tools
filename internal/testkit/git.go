package testkit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/stretchr/testify/require"
)

// GitRig is a bare git remote and clones of it, all under the test's own
// t.TempDir(), for the tests that must run real git (docs/STANDARD.md, section
// 8: shared rigs live in internal/testkit, never copies; section 9 rule 10: a
// test writes only inside its own t.TempDir()). Every command runs with a
// fixed identity and GIT_CONFIG_GLOBAL pointed at an empty file inside the
// rig's directory, through the command's environment, never t.Setenv, so the
// machine's own git identity and config cannot reach the test.
type GitRig struct {
	t      testing.TB
	git    string
	global string
	env    []string
	// Remote is the bare remote's path; Clones are the clones', in the order made.
	Remote string
	Clones []string
}

// gitIdentity is the identity every rig command carries in its environment, so
// a commit needs nothing from the machine.
var gitIdentity = []string{
	"GIT_AUTHOR_NAME=testkit",
	"GIT_AUTHOR_EMAIL=testkit@example.com",
	"GIT_COMMITTER_NAME=testkit",
	"GIT_COMMITTER_EMAIL=testkit@example.com",
	"GIT_CONFIG_NOSYSTEM=1",
}

// Git builds a rig of one bare remote and n clones of it, failing the test when
// git is not on PATH or a git command fails. It runs the git that
// exec.LookPath finds on PATH, the one the test environment provides.
func Git(t testing.TB, n int) *GitRig {
	t.Helper()
	git, err := exec.LookPath("git")
	require.NoError(t, err, "the git rig runs the git on PATH")
	dir := t.TempDir()
	g := &GitRig{t: t, git: git, global: filepath.Join(dir, "gitconfig"), Remote: filepath.Join(dir, "remote")}
	WriteFile(t, g.global, "")
	g.env = gitEnv(g.global)
	g.run(dir, "init", "--bare", g.Remote)
	for i := 0; i < n; i++ {
		clone := filepath.Join(dir, "clone"+strconv.Itoa(i))
		g.run(dir, "clone", g.Remote, clone)
		g.Clones = append(g.Clones, clone)
	}
	return g
}

// gitEnv is the caller's environment without its GIT_ variables, with the rig's
// own identity and global config after them, so the command's environment is
// the rig's and not the machine's.
func gitEnv(global string) []string {
	env := make([]string, 0, len(os.Environ())+len(gitIdentity)+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, gitIdentity...)
	return append(env, "GIT_CONFIG_GLOBAL="+global)
}

// Commit writes files (a path under the clone to its body) into the clone and
// commits them under the rig's fixed identity.
func (g *GitRig) Commit(clone string, files map[string]string) {
	g.t.Helper()
	for name, body := range files {
		WriteFile(g.t, filepath.Join(clone, name), body)
	}
	g.run(clone, "add", "-A")
	g.run(clone, "commit", "-m", "testkit commit")
}

// Push is one git push from the clone to the rig's remote, named by its file
// path under the test's own directory, so the remote's branch follows the
// clone's.
func (g *GitRig) Push(clone string) {
	g.t.Helper()
	g.run(clone, "push", g.Remote, "HEAD")
}

// Head returns the commit HEAD names in repo, the remote's or a clone's.
func (g *GitRig) Head(repo string) string {
	g.t.Helper()
	return strings.TrimSpace(g.run(repo, "rev-parse", "HEAD"))
}

// run runs one git command in dir under the rig's environment through
// internal/gitrun, failing the test on any error and returning its stdout.
func (g *GitRig) run(dir string, args ...string) string {
	g.t.Helper()
	res, err := gitrun.Run(context.Background(), gitrun.Options{Bin: g.git, Dir: dir, Env: g.env}, args...)
	require.NoError(g.t, err, "git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(res.Stderr)))
	return string(res.Stdout)
}
