package testkit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/stretchr/testify/require"
)

// GitRig is a small git rig for the tests that must run real git: a bare
// remote and clones of it, all under the test's own t.TempDir()
// (docs/STANDARD.md, section 9 rule 10). Every command runs through
// internal/gitrun in the rig's environment: a fixed identity,
// GIT_CONFIG_GLOBAL pointed at an empty file inside the rig's directory and
// the system config off, never t.Setenv (docs/STANDARD.md, section 8), so the
// machine's own git identity and config cannot reach the test.
type GitRig struct {
	t      testing.TB
	global string // the empty file the commands' GIT_CONFIG_GLOBAL names
	env    []string
	// Remote is the bare remote's path; Clones are the clones', in the order made.
	Remote string
	Clones []string
}

// The identity every rig command carries, in the command's environment, so a
// commit needs nothing from the machine.
var gitIdentityEnv = [][2]string{
	{"GIT_AUTHOR_NAME", "testkit"},
	{"GIT_AUTHOR_EMAIL", "testkit@example.com"},
	{"GIT_COMMITTER_NAME", "testkit"},
	{"GIT_COMMITTER_EMAIL", "testkit@example.com"},
	{"GIT_CONFIG_NOSYSTEM", "1"},
}

// Git builds a rig of one bare remote and clones clones of it, failing the
// test through require when git does (docs/STANDARD.md, section 8: shared
// rigs live in internal/testkit, never copies).
func Git(t testing.TB, clones int) *GitRig {
	t.Helper()
	dir := t.TempDir()
	g := &GitRig{t: t, global: filepath.Join(dir, "gitconfig"), Remote: filepath.Join(dir, "remote")}
	WriteFile(t, g.global, "")
	g.env = gitEnv()
	g.run(dir, "init", "--bare", g.Remote)
	for i := range clones {
		clone := filepath.Join(dir, fmt.Sprintf("clone%d", i))
		g.run(dir, "clone", g.Remote, clone)
		g.Clones = append(g.Clones, clone)
	}
	return g
}

// gitEnv is the caller's environment without its GIT_ variables, with the
// rig's own set after them.
func gitEnv() []string {
	env := make([]string, 0, len(os.Environ())+len(gitIdentityEnv)+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	for _, kv := range gitIdentityEnv {
		env = append(env, kv[0]+"="+kv[1])
	}
	return append(env, "GIT_CONFIG_GLOBAL=")
}

// Run runs git in repo and returns its stdout, failing the test on any
// error. It is the one raw command of the rig; Commit, Push and Head are its
// named helpers.
func (g *GitRig) Run(repo string, args ...string) string {
	g.t.Helper()
	return g.run(repo, args...)
}

// Commit writes files (path under the clone to body) into the clone and
// commits them under the rig's fixed identity.
func (g *GitRig) Commit(clone string, files map[string]string) {
	g.t.Helper()
	for name, body := range files {
		WriteFile(g.t, filepath.Join(clone, name), body)
	}
	g.run(clone, "add", "-A")
	g.run(clone, "commit", "-m", "testkit commit")
}

// Push moves the clone's current branch to the rig's remote. It fetches the
// branch into the remote instead of running `git push`: a card's git records
// a push for the sprint instead of performing it (docs/SPEC-CARD-CONTRACT.md,
// the push), so the rig reaches its local remote by a real fetch over the
// local path, the way the tree populates a local origin, on the wall and on a
// bench alike.
func (g *GitRig) Push(clone string) {
	g.t.Helper()
	branch := strings.TrimSpace(g.run(clone, "rev-parse", "--abbrev-ref", "HEAD"))
	g.run(g.Remote, "fetch", clone, branch+":refs/heads/"+branch)
}

// Head returns the commit HEAD names in repo, the remote's or a clone's.
func (g *GitRig) Head(repo string) string {
	g.t.Helper()
	return strings.TrimSpace(g.run(repo, "rev-parse", "HEAD"))
}

// run is Run without the helper mark, so a helper's caller is not marked.
func (g *GitRig) run(dir string, args ...string) string {
	env := append(append([]string(nil), g.env...), "GIT_CONFIG_GLOBAL="+g.global)
	res, err := gitrun.Run(context.Background(), gitrun.Options{C: dir, Env: env}, args...)
	require.NoError(g.t, err, "git %s: %s", strings.Join(args, " "), res.Stderr)
	return string(res.Stdout)
}
