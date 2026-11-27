//go:build functional

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// guard_functional_test.go runs commit-guard on real git repositories: in-process on a range, and as the pre-push
// hook roles/push-guard installs, run through ansible-core on localhost with HOME a temp dir, against a local bare
// repository. guard_test.go and push_guard_play_test.go are the unit halves.

// gitEnv is git with nothing from this machine: no system or global config but home's, no prompt.
func gitEnv(home string) []string {
	return append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
}

func gitRun(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed on this machine; guard_test.go still runs")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func gitOK(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	out, err := gitRun(t, dir, env, args...)
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(out)
}

// commitAs makes an empty commit with author and committer the given identity and the given message.
func commitAs(t *testing.T, dir string, env []string, name, mail, msg string) string {
	t.Helper()
	env = append(env, "GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+mail, "GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL="+mail)
	gitOK(t, dir, env, "commit", "-q", "--allow-empty", "-m", msg)
	return gitOK(t, dir, env, "rev-parse", "HEAD")
}

const guardMarked = "fleet: add a package\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"

// On a real repository, a range holding a seat's AI-marked commit, the guarded person's plain commit and an AI-marked
// commit under the guarded address refuses the last alone; the range without it passes.
func TestFunctionalCommitGuardRange(t *testing.T) {
	t.Parallel()
	home, repo := t.TempDir(), t.TempDir()
	env := gitEnv(home)
	gitOK(t, repo, env, "init", "-q")
	base := commitAs(t, repo, env, "bench-1", "bench-1@example.com", "init")
	commitAs(t, repo, env, "bench-1", "bench-1@example.com", guardMarked)
	plain := commitAs(t, repo, env, "Alice", "alice@example.com", "docs: a typo")
	marked := commitAs(t, repo, env, "Alice", "Alice@Example.com", guardMarked)
	rules := filepath.Join(home, "rules")
	require.NoError(t, os.WriteFile(rules, []byte(guardTestRules), 0o600))

	var out, errb bytes.Buffer
	code := commitGuard([]string{"--rules", rules, "--repo", repo, base + "..HEAD"}, strings.NewReader(""), outCmd, &out, &errb)
	assert.Equal(t, 1, code)
	assert.Empty(t, out.String())
	assert.Equal(t, "nova-fleet commit-guard: commit "+marked+" (author Alice <Alice@Example.com>, committer Alice <Alice@Example.com>) "+
		`carries the AI marker "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" under a guarded address; `+
		"amend it as the seat's own identity (git config user.email <the seat's address>), run: git rebase --exec 'git commit --amend --no-edit --reset-author' "+marked+"~1\n", errb.String())

	out.Reset()
	errb.Reset()
	code = commitGuard([]string{"--rules", rules, "--repo", repo, base + ".." + plain}, strings.NewReader(""), outCmd, &out, &errb)
	assert.Equal(t, 0, code, errb.String())
	assert.Equal(t, "COMMIT-GUARD OK commits=2\n", out.String())
}

var (
	fleetBuild     sync.Once
	fleetBuildPath string
	fleetBuildErr  error
)

// builtFleet is this package built once into a temp dir, as install puts nova-fleet in nova_bin_dir.
func builtFleet(t *testing.T) string {
	t.Helper()
	fleetBuild.Do(func() {
		dir, err := os.MkdirTemp("", "nova-fleet-guard-bin-")
		if err != nil {
			fleetBuildErr = err
			return
		}
		fleetBuildPath = filepath.Join(dir, "nova-fleet")
		out, err := exec.Command("go", "build", "-o", fleetBuildPath, ".").CombinedOutput()
		if err != nil {
			fleetBuildErr = &exec.Error{Name: "go build: " + string(out), Err: err}
		}
	})
	require.NoError(t, fleetBuildErr)
	return fleetBuildPath
}

const pushGuardPlay = `- hosts: localhost
  connection: local
  gather_facts: true
  gather_subset: [min]
  vars_files: ["{{ fleet }}/group_vars/all.yml"]
  vars:
    nova_push_guard_people: [alice@example.com]
    nova_push_guard_markers: ["Co-Authored-By: Claude", "Generated with [Claude Code]"]
  tasks:
    - ansible.builtin.include_role: { name: push-guard }
`

func runPushGuardPlay(t *testing.T, home, bin string, extra ...string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine; push_guard_play_test.go still runs")
	}
	fleet, err := filepath.Abs(".")
	require.NoError(t, err)
	dir := t.TempDir()
	play := filepath.Join(dir, "push-guard.yml")
	require.NoError(t, os.WriteFile(play, []byte(pushGuardPlay), 0o644))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	args := append([]string{"-i", "localhost,", "-e", "fleet=" + fleet, "-e", "nova_bin_dir=" + bin}, extra...)
	cmd := exec.CommandContext(ctx, "ansible-playbook", append(args, play)...)
	cmd.Env = append(gitEnv(home), "ANSIBLE_NOCOLOR=1", "ANSIBLE_LOCAL_TEMP="+filepath.Join(dir, "tmp"),
		"ANSIBLE_ROLES_PATH="+filepath.Join(fleet, "roles"), "TMPDIR="+dir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// The play hooks a login: a push sending an AI-marked commit under the guarded address is refused at the hook and
// the remote is unchanged; the person's plain commit and the seat's own AI-marked commit push; the repository's own
// pre-push and pre-commit hooks still run; a second run and --check change nothing.
func TestFunctionalPushGuardRefusesAtPush(t *testing.T) {
	t.Parallel()
	bin := filepath.Dir(builtFleet(t))
	home := t.TempDir()
	out, err := runPushGuardPlay(t, home, bin)
	require.NoError(t, err, out)
	assert.Contains(t, out, "PUSH-GUARD host=localhost login=")
	assert.Contains(t, out, "identity=unset changed")
	hooks := filepath.Join(home, ".config", "nova-fleet", "git-hooks")
	env := gitEnv(home)
	assert.Equal(t, hooks, gitOK(t, home, env, "config", "--global", "core.hooksPath"))
	rules, err := os.ReadFile(filepath.Join(home, ".config", "nova-fleet", "commit-guard"))
	require.NoError(t, err)
	assert.Contains(t, string(rules), "\nperson alice@example.com\nmarker Co-Authored-By: Claude\nmarker Generated with [Claude Code]\n")

	remote, repo := filepath.Join(t.TempDir(), "remote.git"), t.TempDir()
	assert.Empty(t, gitOK(t, home, env, "init", "-q", "--bare", remote), "the hooks print nothing while a repository is made")
	assert.Empty(t, gitOK(t, repo, env, "init", "-q", "-b", "main"), "the hooks print nothing while a repository is made")
	gitOK(t, repo, env, "remote", "add", "origin", remote)
	own := filepath.Join(repo, ".git", "hooks")
	require.NoError(t, os.WriteFile(filepath.Join(own, "pre-push"), []byte("#!/bin/sh\necho OWN-PRE-PUSH \"$1\"; cat >/dev/null\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(own, "pre-commit"), []byte("#!/bin/sh\necho OWN-PRE-COMMIT\n"), 0o755))

	commitAs(t, repo, env, "bench-1", "bench-1@example.com", guardMarked)
	commitAs(t, repo, env, "Alice", "alice@example.com", "docs: a typo")
	got := gitOK(t, repo, env, "push", "origin", "main")
	assert.Contains(t, got, "OWN-PRE-PUSH origin", "the repository's own pre-push still runs")
	pushed := gitOK(t, repo, env, "rev-parse", "HEAD")

	env2 := append(env, "GIT_AUTHOR_NAME=Alice", "GIT_AUTHOR_EMAIL=alice@example.com", "GIT_COMMITTER_NAME=Alice", "GIT_COMMITTER_EMAIL=alice@example.com")
	got = gitOK(t, repo, env2, "commit", "--allow-empty", "-m", guardMarked)
	assert.Contains(t, got, "OWN-PRE-COMMIT", "the repository's own pre-commit still runs")
	marked := gitOK(t, repo, env, "rev-parse", "HEAD")
	got, err = gitRun(t, repo, env, "push", "origin", "main")
	require.Error(t, err, got)
	assert.Contains(t, got, "nova-fleet commit-guard: commit "+marked+" (author Alice <alice@example.com>")
	assert.Contains(t, got, "run: git rebase --exec 'git commit --amend --no-edit --reset-author' "+marked+"~1")
	assert.NotContains(t, got, "OWN-PRE-PUSH", "the guard refused before the repository's own hook")
	assert.Equal(t, pushed, gitOK(t, remote, env, "rev-parse", "main"), "the refused push changed the remote")

	envSeat := append(env, "GIT_AUTHOR_NAME=bench-1", "GIT_AUTHOR_EMAIL=bench-1@example.com", "GIT_COMMITTER_NAME=bench-1", "GIT_COMMITTER_EMAIL=bench-1@example.com")
	gitOK(t, repo, envSeat, "commit", "-q", "--amend", "--allow-empty", "--no-edit", "--reset-author")
	gitOK(t, repo, env, "push", "origin", "main")
	assert.Equal(t, gitOK(t, repo, env, "rev-parse", "HEAD"), gitOK(t, remote, env, "rev-parse", "main"), "the amended commit pushes")

	out, err = runPushGuardPlay(t, home, bin)
	require.NoError(t, err, out)
	assert.Contains(t, out, "changed=0", "a second run changed something")
	assert.Contains(t, out, "identity=unset unchanged")
	out, err = runPushGuardPlay(t, home, bin, "--check", "--diff")
	require.NoError(t, err, out)
	assert.Contains(t, out, "changed=0")
}

// --check on a fresh login writes nothing; a login whose core.hooksPath names another directory, or whose nova-fleet
// lacks commit-guard, fails before anything is written; a skipped machine is left as it is.
func TestFunctionalPushGuardRefusals(t *testing.T) {
	t.Parallel()
	bin := filepath.Dir(builtFleet(t))
	fresh := t.TempDir()
	out, err := runPushGuardPlay(t, fresh, bin, "--check", "--diff")
	require.NoError(t, err, out)
	assert.Contains(t, out, "would-change")
	_, err = os.Stat(filepath.Join(fresh, ".config", "nova-fleet"))
	assert.True(t, os.IsNotExist(err), "--check wrote the hooks")
	_, err = os.Stat(filepath.Join(fresh, ".gitconfig"))
	assert.True(t, os.IsNotExist(err), "--check wrote ~/.gitconfig")

	taken := t.TempDir()
	gitOK(t, taken, gitEnv(taken), "config", "--global", "core.hooksPath", "/elsewhere")
	out, err = runPushGuardPlay(t, taken, bin)
	require.Error(t, err, out)
	assert.Contains(t, out, "PUSH-GUARD host=localhost HOOKS-PATH-TAKEN: core.hooksPath is /elsewhere")
	_, err = os.Stat(filepath.Join(taken, ".config", "nova-fleet"))
	assert.True(t, os.IsNotExist(err), "a refused run wrote the hooks")

	out, err = runPushGuardPlay(t, fresh, t.TempDir())
	require.Error(t, err, out)
	assert.Contains(t, out, "PUSH-GUARD host=localhost NO-VERB:")

	out, err = runPushGuardPlay(t, fresh, bin, "-e", `{"nova_push_guard_skip": ["localhost"]}`)
	require.NoError(t, err, out)
	assert.Contains(t, out, "PUSH-GUARD host=localhost SKIPPED")
	_, err = os.Stat(filepath.Join(fresh, ".config", "nova-fleet"))
	assert.True(t, os.IsNotExist(err), "a skipped machine was written")
}
