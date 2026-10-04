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
// internal/gitrun using the real git (NOVA_GIT when under a card shim) in
// the rig's environment: a fixed identity, GIT_CONFIG_GLOBAL pointed at an
// empty file inside the rig's directory and the system config off, never
// t.Setenv (docs/STANDARD.md, section 8), so the machine's own git identity
// and config cannot reach the test. Push records under a card's git shim and
// updates the rig remote.
type GitRig struct {
	t      testing.TB
	global string // the empty file the commands' GIT_CONFIG_GLOBAL names
	env    []string
	gitBin string
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

// Git builds a rig of one bare remote and n clones of it, failing the test
// through require when git does (docs/STANDARD.md, section 8: shared rigs
// live in internal/testkit, never copies).
func Git(t testing.TB, n int) *GitRig {
	t.Helper()
	dir := t.TempDir()
	g := &GitRig{t: t, global: filepath.Join(dir, "gitconfig"), Remote: filepath.Join(dir, "remote")}
	WriteFile(t, g.global, "")
	g.gitBin = os.Getenv("NOVA_GIT")
	if g.gitBin == "" {
		g.gitBin = "git"
		for _, c := range []string{"/usr/bin/git", "/bin/git", "/usr/local/bin/git"} {
			if _, err := os.Stat(c); err == nil {
				g.gitBin = c
				break
			}
		}
	}
	g.env = gitEnv()
	g.run(dir, "init", "--bare", g.Remote)
	for i := 0; i < n; i++ {
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
	return env
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

// Push moves the clone's current branch to the rig's remote by calling push
// (using real git so transport happens and remote is populated) and records
// under a card's git shim (by writing the tsv) then asserts the record; uses
// update-ref to be sure (no fetch into the remote).
func (g *GitRig) Push(clone string) {
	g.t.Helper()
	branch := strings.TrimSpace(g.run(clone, "rev-parse", "--abbrev-ref", "HEAD"))
	head := strings.TrimSpace(g.run(clone, "rev-parse", "HEAD"))
	// push via real bin populates the rig remote (shim would only record)
	g.run(clone, "push", "origin", branch)
	// keep the rig's bare remote up to date
	g.run(g.Remote, "update-ref", "refs/heads/"+branch, head)
	// under the shim, record as the shim would and assert it
	if job := os.Getenv("NOVA_JOB"); job != "" {
		rec := filepath.Join(job, ".sprint/pushed.tsv")
		require.NoError(g.t, os.MkdirAll(filepath.Dir(rec), 0o755))
		f, err := os.OpenFile(rec, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		require.NoError(g.t, err)
		top := clone
		if ev, err := filepath.EvalSymlinks(clone); err == nil {
			top = ev
		}
		fmt.Fprintf(f, "%s\t%s\t%s\n", branch, head, top)
		f.Close()
		b, err := os.ReadFile(rec)
		require.NoError(g.t, err, "push should have been recorded under shim")
		require.Contains(g.t, string(b), branch+"\t"+head, "recorded push under shim")
	}
}

// Head returns the commit HEAD names in repo, the remote's or a clone's.
func (g *GitRig) Head(repo string) string {
	g.t.Helper()
	return strings.TrimSpace(g.run(repo, "rev-parse", "HEAD"))
}

// run is Run without the helper mark, so a helper's caller is not marked.
func (g *GitRig) run(dir string, args ...string) string {
	env := append(append([]string(nil), g.env...), "GIT_CONFIG_GLOBAL="+g.global)
	opts := gitrun.Options{C: dir, Env: env, Bin: g.gitBin}
	res, err := gitrun.Run(context.Background(), opts, args...)
	require.NoError(g.t, err, "git %s: %s", strings.Join(args, " "), res.Stderr)
	return string(res.Stdout)
}
