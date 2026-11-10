//go:build functional

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ssh_names_play_functional_test.go runs ssh-names.yml through ansible-core against an inventory of its own: two
// machines reached with the local connection, each with its own home (nova_home), and a laptop whose ssh is `false`,
// so it never answers. The mesh's ssh is a fake on PATH that answers every name but the one FAKE_SSH_FAIL names.

// fakeSSH succeeds unless one of its arguments is $FAKE_SSH_FAIL, and then fails as an unreachable port does.
const fakeSSH = `#!/bin/sh
for a in "$@"; do
  if [ -n "$FAKE_SSH_FAIL" ] && [ "$a" = "$FAKE_SSH_FAIL" ]; then
    echo "ssh: connect to host $a port 22: Connection timed out" >&2
    exit 255
  fi
done
exit 0
`

type sshNamesFleet struct {
	dir, inventory, bin string
	home                map[string]string
}

func newSSHNamesFleet(t *testing.T) *sshNamesFleet {
	t.Helper()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	never, err := exec.LookPath("false")
	require.NoError(t, err)
	f := &sshNamesFleet{dir: t.TempDir(), home: map[string]string{}}
	f.bin = filepath.Join(f.dir, "bin")
	require.NoError(t, os.MkdirAll(f.bin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.bin, "ssh"), []byte(fakeSSH), 0o755))
	var inv strings.Builder
	inv.WriteString("all:\n  hosts:\n")
	for _, h := range []struct{ name, user string }{{"bench-1", "alice"}, {"mac-1", "bob"}} {
		f.home[h.name] = filepath.Join(f.dir, "home-"+h.name)
		require.NoError(t, os.MkdirAll(filepath.Join(f.home[h.name], ".ssh"), 0o700))
		fmt.Fprintf(&inv, "    %s: {ansible_connection: local, ansible_python_interpreter: auto_silent, ansible_host: %s.tail.example, ansible_user: %s, nova_home: %q}\n",
			h.name, h.name, h.user, f.home[h.name])
	}
	fmt.Fprintf(&inv, "    laptop-1: {ansible_host: laptop-1.tail.example, ansible_user: carol, ansible_ssh_executable: %q}\n", never)
	f.inventory = filepath.Join(f.dir, "inventory.yml")
	require.NoError(t, os.WriteFile(f.inventory, []byte(inv.String()), 0o644))
	return f
}

func (f *sshNamesFleet) run(t *testing.T, fail string, extra ...string) (string, error) {
	t.Helper()
	play, err := filepath.Abs("ssh-names.yml")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ansible-playbook", append([]string{"-i", f.inventory, play}, extra...)...)
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "HOME="+f.dir, "TMPDIR="+f.dir,
		"ANSIBLE_LOCAL_TEMP="+filepath.Join(f.dir, "tmp"), "PATH="+f.bin+":"+os.Getenv("PATH"), "FAKE_SSH_FAIL="+fail)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (f *sshNamesFleet) read(t *testing.T, machine, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.home[machine], rel))
	require.NoError(t, err)
	return string(b)
}

const wantSSHNamesConfig = `# nova-fleet ssh-names: one block per machine of the fleet record, written by ` + "`nova-fleet ssh-names`" + `; a run rewrites it.
# ~/.ssh/config includes this file on its first line, so for these names the HostName and User here win over a Host
# block of the same name there, and that block's other options still apply.

Host bench-1 bench-1.tail.example
  HostName bench-1.tail.example
  User alice
  UserKnownHostsFile ~/.ssh/known_hosts ~/.ssh/config.d/nova-fleet.known_hosts

Host laptop-1 laptop-1.tail.example
  HostName laptop-1.tail.example
  User carol
  UserKnownHostsFile ~/.ssh/known_hosts ~/.ssh/config.d/nova-fleet.known_hosts

Host mac-1 mac-1.tail.example
  HostName mac-1.tail.example
  User bob
  UserKnownHostsFile ~/.ssh/known_hosts ~/.ssh/config.d/nova-fleet.known_hosts
`

// Every machine that answers gets one block per machine of the inventory (the laptop asleep included), the Include
// moved to the first line of its ssh config with the hand-written blocks kept, the host keys of the machines that
// answered and the kept line of the one that did not; the laptop is reported offline and the run passes; a second
// run changes nothing.
func TestSSHNamesWritesEveryMachineAndSkipsTheOneAsleep(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t)
	hand := "Host mac-1\n  HostName 192.0.2.5\n  User root\n  IdentityFile ~/.ssh/old\n\nInclude ~/.ssh/config.d/nova-fleet\n"
	require.NoError(t, os.WriteFile(filepath.Join(f.home["bench-1"], ".ssh", "config"), []byte(hand), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(f.home["bench-1"], ".ssh", "config.d"), 0o700))
	kept := "laptop-1,laptop-1.tail.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKeptKeptKeptKeptKeptKeptKeptKeptKeptKeptKe"
	require.NoError(t, os.WriteFile(filepath.Join(f.home["bench-1"], ".ssh", "config.d", "nova-fleet.known_hosts"),
		[]byte(kept+"\nmac-1,mac-1.tail.example ssh-ed25519 AAAAstale\n"), 0o600))

	out, err := f.run(t, "")
	require.NoError(t, err, out)
	assert.Contains(t, out, "SSH-NAMES laptop-1 OFFLINE")
	for _, m := range []string{"bench-1", "mac-1"} {
		assert.Contains(t, out, "SSH-NAMES MESH "+m+" ok=1 offline=laptop-1 failed=-")
		assert.Equal(t, wantSSHNamesConfig, f.read(t, m, ".ssh/config.d/nova-fleet"), m)
	}
	assert.Equal(t, "Include ~/.ssh/config.d/nova-fleet\n"+strings.TrimSuffix(hand, "Include ~/.ssh/config.d/nova-fleet\n"),
		f.read(t, "bench-1", ".ssh/config"), "the Include first, once; the hand-written block kept")
	assert.Equal(t, "Include ~/.ssh/config.d/nova-fleet\n", f.read(t, "mac-1", ".ssh/config"))

	known := f.read(t, "bench-1", ".ssh/config.d/nova-fleet.known_hosts")
	assert.Contains(t, known, kept+"\n", "the line of a machine that did not answer is kept")
	assert.NotContains(t, known, "AAAAstale", "a machine that answered has its key read again")
	assert.NotContains(t, f.read(t, "mac-1", ".ssh/config.d/nova-fleet.known_hosts"), "laptop-1", "no line is made up for a machine never seen")
	if pub, err := os.ReadFile("/etc/ssh/ssh_host_ed25519_key.pub"); err == nil {
		key := strings.Fields(string(pub))[1]
		assert.Contains(t, known, "bench-1,bench-1.tail.example ssh-ed25519 "+key+"\n")
		assert.Contains(t, known, "mac-1,mac-1.tail.example ssh-ed25519 "+key+"\n")
	}

	out, err = f.run(t, "")
	require.NoError(t, err, out)
	assert.Contains(t, out, "SSH-NAMES bench-1 machines=3 config=same known_hosts=same include=first")
	for _, m := range []string{"bench-1", "mac-1"} {
		assert.Regexp(t, m+` +: ok=\d+ +changed=0 `, out, "a second run changed something")
	}
}

// A pair that fails between two machines that both answered fails the run, naming the target and ssh's own line.
func TestSSHNamesMeshFailureFailsTheRun(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t)
	out, err := f.run(t, "mac-1")
	require.Error(t, err, out)
	assert.Contains(t, out, "SSH-NAMES MESH bench-1 ok=0 offline=laptop-1 failed=mac-1")
	assert.Contains(t, out, "SSH-NAMES MESH bench-1 FAILED failed mac-1: ssh: connect to host mac-1 port 22: Connection timed out")
	assert.Contains(t, out, "SSH-NAMES MESH mac-1 ok=1 offline=laptop-1 failed=-")
}

// --check writes nothing and checks no pair.
func TestSSHNamesCheckWritesNothing(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t)
	out, err := f.run(t, "", "--check")
	require.NoError(t, err, out)
	assert.Contains(t, out, "SSH-NAMES bench-1 machines=3 config=would-be-written known_hosts=would-be-written include=would-be-set (check: nothing changed)")
	assert.NotContains(t, out, "SSH-NAMES MESH")
	for _, m := range []string{"bench-1", "mac-1"} {
		entries, err := os.ReadDir(filepath.Join(f.home[m], ".ssh"))
		require.NoError(t, err)
		assert.Empty(t, entries, "--check wrote into %s's ~/.ssh", m)
	}
}
