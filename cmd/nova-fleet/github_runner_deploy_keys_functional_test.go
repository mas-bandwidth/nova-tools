//go:build functional

package main

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// github_runner_deploy_keys_functional_test.go runs roles/github-runner/tasks/deploy-keys.yml on localhost with
// ansible-core, as the test's own login, in a temporary home: a fake nova-secrets (names lists one file, exec hands one
// value in the environment) and a fake git on PATH whose ls-remote asks the real git what the URL resolves to and
// answers without the network. No become: ansible_become is false for the run.

const deployKeysPlay = `- hosts: localhost
  connection: local
  gather_facts: true
  gather_subset: [min]
  vars_files: ["{{ fleet }}/group_vars/all.yml", "{{ fleet }}/roles/github-runner/defaults/main.yml"]
  tasks:
    - ansible.builtin.include_tasks: "{{ fleet }}/roles/github-runner/tasks/deploy-keys.yml"
`

// The fake nova-secrets: names prints $FAKE_NAMES; exec takes the name after --only and runs what follows -- with that
// name set to the contents of $FAKE_VALUE.
const fakeNovaSecrets = `#!/bin/sh
case "$1" in
names) cat "$FAKE_NAMES"; exit 0 ;;
exec) ;;
*) echo "fake nova-secrets: $1" >&2; exit 2 ;;
esac
name=
while [ $# -gt 0 ]; do
  case "$1" in
  --only) name=$2; shift 2 ;;
  --) shift; break ;;
  *) shift ;;
  esac
done
echo "SECRETS EXEC STARTING as=fake keys=1 cmd=$1" >&2
export "$name=$(cat "$FAKE_VALUE")"
exec "$@"
`

// The fake git: ls-remote <url> HEAD writes what the real git resolves the URL to and prints a HEAD line.
const fakeGit = `#!/bin/sh
[ "$1" = ls-remote ] || exec "$REAL_GIT" "$@"
"$REAL_GIT" ls-remote --get-url "$2" > "$FAKE_RESOLVED" || exit 1
printf '0000000000000000000000000000000000000000\tHEAD\n'
`

type deployKeysRun struct {
	dir, home, key, value string
}

func newDeployKeysRun(t *testing.T) deployKeysRun {
	t.Helper()
	for _, tool := range []string{"ansible-playbook", "ssh-keygen", "git", "ssh"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed on this machine; TestDeployKeysScopeAndSecrecy still runs")
		}
	}
	dir := t.TempDir()
	r := deployKeysRun{dir: dir, home: filepath.Join(dir, "home"), key: filepath.Join(dir, "key")}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bin"), 0o755))
	require.NoError(t, os.MkdirAll(r.home, 0o755))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "test", "-f", r.key).CombinedOutput()
	require.NoError(t, err, string(out))
	priv, err := os.ReadFile(r.key)
	require.NoError(t, err)
	r.value = base64.StdEncoding.EncodeToString(priv)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "value"), []byte(r.value), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bin", "nova-secrets"), []byte(fakeNovaSecrets), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bin", "git"), []byte(fakeGit), 0o755))
	return r
}

func (r deployKeysRun) play(t *testing.T, names string, extra ...string) (string, error) {
	t.Helper()
	fleet, err := filepath.Abs(".")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(r.dir, "names"), []byte(names), 0o644))
	play := filepath.Join(r.dir, "deploy-keys.yml")
	require.NoError(t, os.WriteFile(play, []byte(deployKeysPlay), 0o644))
	me, err := user.Current()
	require.NoError(t, err)
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	args := append([]string{"-i", "localhost,", "-e", "fleet=" + fleet, "-e", "ansible_become=false",
		"-e", "github_runner_user=" + me.Username, "-e", "github_runner_home=" + r.home,
		"-e", "github_runner_nova_secrets=" + filepath.Join(r.dir, "bin", "nova-secrets"),
		"-e", `{"github_runner_deploy_keys": [{"url": "https://github.com/o/r.git", "secret": "DEPLOY_O_R"}]}`}, extra...)
	cmd := exec.CommandContext(ctx, "ansible-playbook", append(args, play)...)
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "HOME="+r.home, "ANSIBLE_LOCAL_TEMP="+filepath.Join(r.dir, "tmp"),
		"PATH="+filepath.Join(r.dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"REAL_GIT="+realGit, "FAKE_NAMES="+filepath.Join(r.dir, "names"), "FAKE_VALUE="+filepath.Join(r.dir, "value"),
		"FAKE_RESOLVED="+filepath.Join(r.dir, "resolved"))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// A run writes the key (0600), the alias that offers it alone and the rewrite of exactly that URL; git resolves the
// URL to the alias for fetching and keeps a push on https; a second run changes nothing; the value is never printed.
func TestFunctionalDeployKeysInstallScopedToOneURL(t *testing.T) {
	t.Parallel()
	r := newDeployKeysRun(t)

	out, err := r.play(t, "DEPLOY_O_R\n", "--check")
	require.NoError(t, err, out)
	assert.Contains(t, out, "DEPLOY-KEY-PLAN localhost")
	assert.Contains(t, out, "key=absent")
	assert.NoFileExists(t, filepath.Join(r.home, ".ssh", "deploy-key-o-r"), "--check writes no key")
	assert.NoFileExists(t, filepath.Join(r.home, ".gitconfig"), "--check writes no config")

	out, err = r.play(t, "DEPLOY_O_R\n")
	require.NoError(t, err, out)
	assert.Contains(t, out, "DEPLOY-KEY-SET deploy-key-o-r")
	assert.Contains(t, out, "DEPLOY-KEY localhost")
	assert.Contains(t, out, "https://github.com/o/r.git ls-remote=ok")
	assert.NotContains(t, out, r.value, "the value is never printed")
	assert.NotContains(t, out, "PRIVATE KEY")

	keyFile := filepath.Join(r.home, ".ssh", "deploy-key-o-r")
	got, err := os.ReadFile(keyFile)
	require.NoError(t, err)
	want, err := os.ReadFile(r.key)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	st, err := os.Stat(keyFile)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())

	resolved, err := os.ReadFile(filepath.Join(r.dir, "resolved"))
	require.NoError(t, err)
	assert.Equal(t, "git@deploy-key-o-r:o/r.git\n", string(resolved), "the proof went through the rewrite")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	git := func(args ...string) string {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(r.home, ".gitconfig"), "GIT_CONFIG_NOSYSTEM=1")
		b, err := cmd.CombinedOutput()
		require.NoError(t, err, string(b))
		return strings.TrimSpace(string(b))
	}
	assert.Equal(t, "https://github.com/o/r2.git", git("ls-remote", "--get-url", "https://github.com/o/r2.git"), "another repository is not rewritten")
	repo := filepath.Join(r.dir, "repo")
	git("init", "-q", repo)
	git("-C", repo, "remote", "add", "origin", "https://github.com/o/r.git")
	assert.Equal(t, "git@deploy-key-o-r:o/r.git", git("-C", repo, "remote", "get-url", "origin"))
	assert.Equal(t, "https://github.com/o/r.git", git("-C", repo, "remote", "get-url", "--push", "origin"), "a push stays on https")

	b, err := exec.CommandContext(ctx, "ssh", "-F", filepath.Join(r.home, ".ssh", "config"), "-G", "deploy-key-o-r").CombinedOutput()
	require.NoError(t, err, string(b))
	cfg := string(b)
	for _, line := range []string{"hostname github.com", "user git", "identityfile " + keyFile, "identitiesonly yes",
		"stricthostkeychecking true", "batchmode yes", "userknownhostsfile " + filepath.Join(r.home, ".ssh", "deploy-keys.known_hosts"),
		"hostkeyalias github.com"} {
		assert.Contains(t, cfg, "\n"+line+"\n")
	}

	out, err = r.play(t, "DEPLOY_O_R\n")
	require.NoError(t, err, out)
	assert.Contains(t, out, "DEPLOY-KEY-SAME deploy-key-o-r")
	assert.Regexp(t, `localhost\s+: ok=\d+\s+changed=0 `, out, "a second run changes nothing")
}

// A key the seat does not hold fails the run by name, with the grant that fixes it, before anything is written.
func TestFunctionalDeployKeysRefuseAKeyTheSeatLacks(t *testing.T) {
	t.Parallel()
	r := newDeployKeysRun(t)
	out, err := r.play(t, "OTHER\n")
	require.Error(t, err, out)
	assert.Contains(t, out, "DEPLOY-KEYS localhost: seat localhost holds no DEPLOY_O_R")
	assert.Contains(t, out, "nova-secrets grant DEPLOY_O_R --to localhost")
	assert.NoDirExists(t, filepath.Join(r.home, ".ssh"))
}

// A value that is no private key never replaces the file, and fails the run.
func TestFunctionalDeployKeysRefuseAValueThatIsNoKey(t *testing.T) {
	t.Parallel()
	r := newDeployKeysRun(t)
	require.NoError(t, os.WriteFile(filepath.Join(r.dir, "value"), []byte(base64.StdEncoding.EncodeToString([]byte("not a key\n"))), 0o600))
	out, err := r.play(t, "DEPLOY_O_R\n")
	require.Error(t, err, out)
	assert.Contains(t, out, "DEPLOY-KEY-BAD deploy-key-o-r: the value is no private key ssh reads")
	assert.NoFileExists(t, filepath.Join(r.home, ".ssh", "deploy-key-o-r"))
	assert.NoFileExists(t, filepath.Join(r.home, ".ssh", "deploy-key-o-r.new"))
}
