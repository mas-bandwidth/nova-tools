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
	"gopkg.in/yaml.v3"
)

// control_key_functional_test.go runs, through ansible-core on this machine with the local connection, the tasks that
// keep the control node the one way in once Tailscale SSH is off: the controlkey role (its key read before anything
// changes) and join's authorized keys for the login; tailscale_ssh_off_functional_test.go has the tailscale role's.
// Each writes only inside the test's own directory: every authorized_keys path is the test's.

const (
	testControlKey  = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIcontrolKey control@node"
	testControlKey2 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIsecondKey second@node"
	testFirstKey    = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIproviderKey provider@console"
	testStrayKey    = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIbenchKey nova@bench-2"
)

// fakeSSHAdd lists the file $FAKE_AGENT names as ssh-add -L does, or says the agent has none (exit 1); every call is
// logged.
const fakeSSHAdd = `#!/bin/sh
echo "$*" >> "$FAKE_SSH_ADD_LOG"
if [ -s "$FAKE_AGENT" ]; then cat "$FAKE_AGENT"; exit 0; fi
echo "The agent has no identities."
exit 1
`

type playRun struct{ dir, bin, agent, log string }

func newPlayRun(t *testing.T) *playRun {
	t.Helper()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	r := &playRun{dir: t.TempDir()}
	r.bin, r.agent, r.log = filepath.Join(r.dir, "bin"), filepath.Join(r.dir, "agent"), filepath.Join(r.dir, "ssh-add.log")
	require.NoError(t, os.MkdirAll(r.bin, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(r.dir, ".ssh"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(r.bin, "ssh-add"), []byte(fakeSSHAdd), 0o755))
	return r
}

// play writes tasks as a play on localhost and runs it with the extra -e JSON, returning its output.
func (r *playRun) play(t *testing.T, vars map[string]any, facts bool, tasks []map[string]any, extra ...string) (string, error) {
	t.Helper()
	roles, err := filepath.Abs("roles")
	require.NoError(t, err)
	b, err := yaml.Marshal([]map[string]any{{"hosts": "localhost", "connection": "local", "gather_facts": facts, "gather_subset": []string{"min"}, "vars": vars, "tasks": tasks}})
	require.NoError(t, err)
	path := filepath.Join(r.dir, "play.yml")
	require.NoError(t, os.WriteFile(path, b, 0o644))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ansible-playbook", append([]string{"-i", "localhost,", path}, extra...)...)
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "ANSIBLE_ROLES_PATH="+roles, "TMPDIR="+r.dir, "ANSIBLE_LOCAL_TEMP="+filepath.Join(r.dir, "tmp"),
		"ANSIBLE_PYTHON_INTERPRETER=auto_silent", "ANSIBLE_ASYNC_DIR="+filepath.Join(r.dir, "async"), "PATH="+r.bin+":"+os.Getenv("PATH"), "FAKE_AGENT="+r.agent, "FAKE_SSH_ADD_LOG="+r.log)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (r *playRun) read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, rel))
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return string(b)
}

var controlKeyTasks = []map[string]any{
	{"ansible.builtin.include_role": map[string]any{"name": "controlkey"}},
	{"ansible.builtin.debug": map[string]any{"msg": "KEY={{ nova_control_key }}"}},
}

// The key is nova_control_ssh_pubkey when given (ssh-add is not asked), else the agent's key when it holds exactly one.
// An agent holding several is refused, never guessed from; with no key, or a value that is not one public key line,
// the run stops there, before the next task, with the remedy.
func TestFunctionalControlKeyIsTheGivenOrTheAgentsOnly(t *testing.T) {
	t.Parallel()
	r := newPlayRun(t)
	require.NoError(t, os.WriteFile(r.agent, []byte(testControlKey+"\n"), 0o600))
	out, err := r.play(t, nil, false, controlKeyTasks)
	require.NoError(t, err, out)
	assert.Contains(t, out, `"msg": "KEY=`+testControlKey+`"`)

	require.NoError(t, os.WriteFile(r.agent, []byte(testControlKey+"\n"+testControlKey2+"\n"), 0o600))
	out, err = r.play(t, nil, false, controlKeyTasks)
	require.Error(t, err, out)
	assert.Contains(t, out, "CONTROLKEY STOP localhost: no one public key for the control node to log in with (ssh-add -L lists 2 keys (control@node, second@node) and nova_control_ssh_pubkey does not say which); nothing was changed.")
	assert.Contains(t, out, "run: ssh-add -L")
	assert.NotContains(t, out, "KEY=")

	require.NoError(t, os.Remove(r.log))
	out, err = r.play(t, nil, false, controlKeyTasks, "-e", "nova_control_ssh_pubkey='"+testControlKey2+"'")
	require.NoError(t, err, out)
	assert.Contains(t, out, `"msg": "KEY=`+testControlKey2+`"`)
	assert.Empty(t, r.read(t, "ssh-add.log"), "a given key: the agent is not asked")

	require.NoError(t, os.Remove(r.agent))
	out, err = r.play(t, nil, false, controlKeyTasks)
	require.Error(t, err, out)
	assert.Contains(t, out, "CONTROLKEY STOP localhost: no one public key for the control node to log in with (ssh-add -L listed none: The agent has no identities.); nothing was changed.")
	assert.NotContains(t, out, "KEY=")

	out, err = r.play(t, nil, false, controlKeyTasks, "-e", "nova_control_ssh_pubkey='SHA256:abcdef'")
	require.Error(t, err, out)
	assert.Contains(t, out, "CONTROLKEY STOP localhost: no one public key for the control node to log in with (nova_control_ssh_pubkey is not one public key line: SHA256:abcdef)")
}

// join's task, as join.yml has it: the login's authorized_keys become exactly the first login's keys (from the machine,
// or the control node's file nova_join_authorized_keys names) and the control node's key; a key planted there is gone;
// a second run changes nothing.
func TestFunctionalJoinAuthorizesExactlyTheFirstKeysAndTheControlNode(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("join.yml")
	require.NoError(t, err)
	var plays []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &plays))
	var task map[string]any
	for _, p := range plays {
		if asText(p["name"]) == "the login (Linux, as root, or as the first login through sudo)" {
			tasks := flattenTasks(t, toTasks(p["tasks"]))
			if i := taskIndex(tasks, "the authorized keys of {{ join_login }}"); i >= 0 {
				task = tasks[i]
			}
		}
	}
	require.NotNil(t, task, "join.yml's login play has no task giving the login its keys")
	me, err := user.Current()
	require.NoError(t, err)

	for _, from := range []string{"the machine", "a file on the control node"} {
		t.Run(from, func(t *testing.T) {
			t.Parallel()
			r := newPlayRun(t)
			keys := filepath.Join(r.dir, ".ssh", "authorized_keys")
			require.NoError(t, os.WriteFile(keys, []byte(testStrayKey+"\n"), 0o600))
			first := "# the provider's key\n" + testFirstKey + "\n"
			vars := map[string]any{"join_login": me.Username, "join_user": map[string]any{"home": r.dir}, "join_ssh_dir": map[string]any{"changed": false}}
			var extra []string
			if from == "the machine" {
				vars["join_first_keys"] = map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(first))}
			} else {
				file := filepath.Join(r.dir, "provider_keys")
				require.NoError(t, os.WriteFile(file, []byte(first), 0o600))
				extra = []string{"-e", "nova_join_authorized_keys=" + file}
			}
			tasks := []map[string]any{{"ansible.builtin.set_fact": map[string]any{"nova_control_key": testControlKey}}, task}
			for run := 1; run <= 2; run++ {
				out, err := r.play(t, vars, false, tasks, extra...)
				require.NoError(t, err, out)
				got := r.read(t, ".ssh/authorized_keys")
				assert.Contains(t, got, testFirstKey, "run %d", run)
				assert.Contains(t, got, testControlKey, "run %d", run)
				assert.NotContains(t, got, testStrayKey, "run %d: a key the join did not give stays", run)
				assert.Equal(t, 2, strings.Count(got, "ssh-ed25519 "), "run %d:\n%s", run, got)
				if run == 2 {
					assert.Regexp(t, `localhost +: ok=\d+ +changed=0 `, out, "a second run changed something")
				}
			}
		})
	}
}
