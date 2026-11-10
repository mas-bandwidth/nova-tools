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

// ssh_names_play_functional_test.go runs ssh-names.yml through ansible-core against an inventory of its own, all on
// the local connection: bench-1, a Linux machine whose root home is a directory of the test's (ssh_names_root_home,
// reached without sudo: ssh_names_root_sudo false) and whose fleet login alice has a home of its own (nova_home); mac-1,
// a Mac whose fleet login bob is the owner's; and laptop-1, a Linux machine whose ssh is `false`, so it never answers.
// Each machine reads its host key from a file of the test's (ssh_names_host_key).
//
// On PATH, a fake ssh. `ssh -G` runs the real client over the calling login's ~/.ssh/config with ~/ made $HOME (ssh
// takes ~ from the password database, never $HOME). Any other call is logged; the worker check's (its known hosts
// /dev/null) is denied as sshd denies a key it does not hold, unless FAKE_SSH_WORKER_IN is set; the rest succeed but
// for the address FAKE_SSH_FAIL names, which times out.
const fakeSSH = `#!/bin/sh
if [ "$1" = "-G" ]; then
  t=$(mktemp)
  sed "s#~/#$HOME/#g" "$HOME/.ssh/config" > "$t"
  shift
  "$REAL_SSH" -G -F "$t" "$@"
  rc=$?
  rm -f "$t"
  exit $rc
fi
echo "$*" >> "$FAKE_SSH_LOG"
case "$*" in
  *UserKnownHostsFile=/dev/null*)
    [ -n "$FAKE_SSH_WORKER_IN" ] && exit 0
    echo "root@host: Permission denied (publickey)." >&2
    exit 255 ;;
esac
for a in "$@"; do
  if [ -n "$FAKE_SSH_FAIL" ] && [ "$a" = "$FAKE_SSH_FAIL" ]; then
    echo "ssh: connect to host $a port 22: Connection timed out" >&2
    exit 255
  fi
done
exit 0
`

const sshNamesTailnet = "tail0test.ts.net"

type sshNamesHost struct{ name, address, user, os string }

type sshNamesFleet struct {
	dir, inventory, bin, sshLog string
	root, login, key            map[string]string // root's home (Linux), the fleet login's home, the host key
}

func sshNamesKey(h string) string {
	return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI" + strings.ReplaceAll(h, "-", "") + "Key"
}

// newSSHNamesFleet makes the fleet; edit, when given, changes a machine's record before the inventory is written.
func newSSHNamesFleet(t *testing.T, edit func(h *sshNamesHost)) *sshNamesFleet {
	t.Helper()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	never, err := exec.LookPath("false")
	require.NoError(t, err)
	f := &sshNamesFleet{dir: t.TempDir(), root: map[string]string{}, login: map[string]string{}, key: map[string]string{}}
	f.bin = filepath.Join(f.dir, "bin")
	f.sshLog = filepath.Join(f.dir, "ssh.log")
	require.NoError(t, os.MkdirAll(f.bin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.bin, "ssh"), []byte(fakeSSH), 0o755))
	var inv strings.Builder
	inv.WriteString("all:\n  hosts:\n")
	for _, h := range []sshNamesHost{{"bench-1", "", "alice", "linux"}, {"laptop-1", "", "carol", "linux"}, {"mac-1", "", "bob", "darwin"}} {
		h.address = h.name + "." + sshNamesTailnet
		f.key[h.name] = sshNamesKey(h.name)
		if edit != nil {
			edit(&h)
		}
		if h.name == "laptop-1" {
			fmt.Fprintf(&inv, "    laptop-1: {ansible_host: %q, ansible_user: %q, nova_os: %q, ansible_ssh_executable: %q}\n", h.address, h.user, h.os, never)
			continue
		}
		f.login[h.name] = filepath.Join(f.dir, "home-"+h.name)
		require.NoError(t, os.MkdirAll(filepath.Join(f.login[h.name], ".ssh"), 0o700))
		keyFile := filepath.Join(f.dir, "key-"+h.name+".pub")
		require.NoError(t, os.WriteFile(keyFile, []byte(f.key[h.name]+" root@"+h.name+"\n"), 0o644))
		root := ""
		if h.name == "bench-1" {
			f.root[h.name] = filepath.Join(f.dir, "root-"+h.name)
			require.NoError(t, os.MkdirAll(filepath.Join(f.root[h.name], ".ssh"), 0o700))
			root = fmt.Sprintf(", ssh_names_root_home: %q, ssh_names_root_sudo: false", f.root[h.name])
		}
		fmt.Fprintf(&inv, "    %s: {ansible_connection: local, ansible_python_interpreter: auto_silent, ansible_host: %q, ansible_user: %q, nova_os: %q, nova_home: %q, ssh_names_host_key: %q%s}\n",
			h.name, h.address, h.user, h.os, f.login[h.name], keyFile, root)
	}
	f.inventory = filepath.Join(f.dir, "inventory.yml")
	require.NoError(t, os.WriteFile(f.inventory, []byte(inv.String()), 0o644))
	return f
}

func (f *sshNamesFleet) run(t *testing.T, env []string, extra ...string) (string, error) {
	t.Helper()
	play, err := filepath.Abs("ssh-names.yml")
	require.NoError(t, err)
	realSSH, err := exec.LookPath("ssh")
	require.NoError(t, err, "the play's config check runs the real ssh -G; the client must be installed")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ansible-playbook", append([]string{"-i", f.inventory, play}, extra...)...)
	cmd.Env = append(append(os.Environ(), "ANSIBLE_NOCOLOR=1", "HOME="+f.dir, "TMPDIR="+f.dir,
		"ANSIBLE_LOCAL_TEMP="+filepath.Join(f.dir, "tmp"), "PATH="+f.bin+":"+os.Getenv("PATH"),
		"FAKE_SSH_LOG="+f.sshLog, "REAL_SSH="+realSSH), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

// sshG is real ssh's view of a name from a config whose ~/ is home, read without the network.
func (f *sshNamesFleet) sshG(t *testing.T, home, name string) map[string]string {
	t.Helper()
	ssh, err := exec.LookPath("ssh")
	require.NoError(t, err)
	resolved := filepath.Join(t.TempDir(), "config")
	conf := strings.ReplaceAll(readFile(t, filepath.Join(home, ".ssh", "config")), "~/", home+"/")
	require.NoError(t, os.WriteFile(resolved, []byte(conf), 0o600))
	out, err := exec.Command(ssh, "-G", "-F", resolved, name).CombinedOutput()
	require.NoError(t, err, "ssh -G %s over %s:\n%s", name, home, out)
	got := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(line, " "); ok {
			got[k] = v
		}
	}
	return got
}

func (f *sshNamesFleet) wroteNothing(t *testing.T) {
	t.Helper()
	for _, dir := range []string{f.root["bench-1"], f.login["bench-1"], f.login["mac-1"]} {
		entries, err := os.ReadDir(filepath.Join(dir, ".ssh"))
		require.NoError(t, err)
		assert.Empty(t, entries, "something was written into %s/.ssh", dir)
	}
}

const sshNamesHeader = `# nova-fleet ssh-names: the owner's ssh by machine name, written by ` + "`nova-fleet ssh-names`" + `; a run rewrites it.
# Linux machines are reached as root, Macs as their login, with the owner's agent forwarded: his key never leaves his
# machine. No fleet login (a worker's) is let in by these names. ~/.ssh/config includes this file on its first line.
`

// root's file on a Linux machine: every block as root on Linux (the login on a Mac), the agent forwarded
const wantSSHNamesLinux = sshNamesHeader + `
Host bench-1 bench-1.tail0test.ts.net
  HostName bench-1.tail0test.ts.net
  User root
  ForwardAgent yes
  UserKnownHostsFile ~/.ssh/known_hosts ~/.ssh/nova-fleet.known_hosts

Host laptop-1 laptop-1.tail0test.ts.net
  HostName laptop-1.tail0test.ts.net
  User root
  ForwardAgent yes
  UserKnownHostsFile ~/.ssh/known_hosts ~/.ssh/nova-fleet.known_hosts

Host mac-1 mac-1.tail0test.ts.net
  HostName mac-1.tail0test.ts.net
  User bob
  ForwardAgent yes
  UserKnownHostsFile ~/.ssh/known_hosts ~/.ssh/nova-fleet.known_hosts
`

// the owner's login's file on a Mac: the same, and his key added to his agent from the keychain
const wantSSHNamesMac = sshNamesHeader + `
Host bench-1 bench-1.tail0test.ts.net
  HostName bench-1.tail0test.ts.net
  User root
  ForwardAgent yes
  AddKeysToAgent yes
  IgnoreUnknown UseKeychain
  UseKeychain yes
  UserKnownHostsFile ~/.ssh/known_hosts ~/.ssh/nova-fleet.known_hosts

Host laptop-1 laptop-1.tail0test.ts.net
  HostName laptop-1.tail0test.ts.net
  User root
  ForwardAgent yes
  AddKeysToAgent yes
  IgnoreUnknown UseKeychain
  UseKeychain yes
  UserKnownHostsFile ~/.ssh/known_hosts ~/.ssh/nova-fleet.known_hosts

Host mac-1 mac-1.tail0test.ts.net
  HostName mac-1.tail0test.ts.net
  User bob
  ForwardAgent yes
  AddKeysToAgent yes
  IgnoreUnknown UseKeychain
  UseKeychain yes
  UserKnownHostsFile ~/.ssh/known_hosts ~/.ssh/nova-fleet.known_hosts
`

// On a Linux machine the files are root's and the fleet login's home (the workers') gets none: what an earlier
// release wrote there is removed, the rest of its config kept. On a Mac they are the owner's login's. Real ssh reads
// the names as root (Linux) or the login (Mac) with the agent forwarded, the Mac's with AddKeysToAgent and UseKeychain.
// The control node reaches each machine with its own key; the worker on the Linux machine is denied as root everywhere;
// each machine's own ssh -G agrees; the laptop asleep is offline; a second run changes nothing.
func TestSSHNamesWritesRootsFilesNotTheWorkers(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	alice := filepath.Join(f.login["bench-1"], ".ssh")
	require.NoError(t, os.MkdirAll(filepath.Join(alice, "config.d"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(alice, "config.d", "nova-fleet"), []byte("Host bench-1\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(alice, "config.d", "nova-github-read-x"), []byte("Host github-x\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(alice, "nova-fleet.known_hosts"), []byte("old\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(alice, "config"), []byte("Include ~/.ssh/config.d/nova-fleet\nInclude ~/.ssh/config.d/*\n"), 0o600))
	kept := "laptop-1,laptop-1.tail0test.ts.net " + f.key["laptop-1"]
	require.NoError(t, os.WriteFile(filepath.Join(f.root["bench-1"], ".ssh", "nova-fleet.known_hosts"), []byte(kept+"\n"), 0o600))

	out, err := f.run(t, nil)
	require.NoError(t, err, out)
	rootSSH := filepath.Join(f.root["bench-1"], ".ssh")
	assert.Equal(t, wantSSHNamesLinux, readFile(t, filepath.Join(rootSSH, "config.d", "nova-fleet")))
	assert.Equal(t, "Include ~/.ssh/config.d/nova-fleet\n", readFile(t, filepath.Join(rootSSH, "config")))
	assert.Equal(t, "bench-1,bench-1.tail0test.ts.net "+f.key["bench-1"]+"\n"+kept+"\nmac-1,mac-1.tail0test.ts.net "+f.key["mac-1"]+"\n",
		readFile(t, filepath.Join(rootSSH, "nova-fleet.known_hosts")), "each machine's sshd key; the asleep machine's kept")
	bob := filepath.Join(f.login["mac-1"], ".ssh")
	assert.Equal(t, wantSSHNamesMac, readFile(t, filepath.Join(bob, "config.d", "nova-fleet")))
	assert.Equal(t, "Include ~/.ssh/config.d/nova-fleet\n", readFile(t, filepath.Join(bob, "config")))

	assert.NoFileExists(t, filepath.Join(alice, "config.d", "nova-fleet"), "the worker's copy from an earlier release is removed")
	assert.NoFileExists(t, filepath.Join(alice, "nova-fleet.known_hosts"))
	assert.FileExists(t, filepath.Join(alice, "config.d", "nova-github-read-x"), "the worker's own files are left alone")
	assert.Equal(t, "Include ~/.ssh/config.d/*\n", readFile(t, filepath.Join(alice, "config")), "the Include goes, the rest stays")

	for home, users := range map[string]map[string]string{f.root["bench-1"]: {"bench-1": "root", "mac-1": "bob"}, f.login["mac-1"]: {"bench-1": "root", "laptop-1": "root", "mac-1": "bob"}} {
		for name, user := range users {
			g := f.sshG(t, home, name)
			assert.Equal(t, user, g["user"], "ssh -G %s over %s", name, home)
			assert.Equal(t, name+"."+sshNamesTailnet, g["hostname"])
			assert.Equal(t, "yes", g["forwardagent"])
		}
	}
	assert.Equal(t, "true", f.sshG(t, f.login["mac-1"], "bench-1")["addkeystoagent"])

	assert.Contains(t, out, "SSH-NAMES laptop-1 OFFLINE")
	assert.Contains(t, out, "SSH-NAMES bench-1 as=root machines=3 online=2 config=written known_hosts=written include=set removed=3")
	assert.Contains(t, out, "SSH-NAMES mac-1 as=bob machines=3 online=2 config=written known_hosts=written include=set removed=-")
	assert.Contains(t, out, "SSH-NAMES CONFIG bench-1 ok=3 wrong=-")
	assert.Contains(t, out, "SSH-NAMES CONFIG mac-1 ok=3 wrong=-")
	assert.Contains(t, out, "SSH-NAMES WORKERS bench-1 denied=1 got-in=- unproven=-")
	assert.NotContains(t, out, "SSH-NAMES WORKERS mac-1", "a Mac's login is the owner's, not a worker's")
	assert.Contains(t, out, "SSH-NAMES CONTROL ok=2 failed=- offline=laptop-1")
	log := readFile(t, f.sshLog)
	assert.Contains(t, log, "-o StrictHostKeyChecking=yes -l root bench-1.tail0test.ts.net true", "the control node, as root")
	assert.Contains(t, log, "-o StrictHostKeyChecking=yes -l bob mac-1.tail0test.ts.net true", "the control node, as the Mac's login")
	assert.Contains(t, log, "-o UserKnownHostsFile=/dev/null -l root mac-1.tail0test.ts.net true", "the worker on bench-1, as root to mac-1")
	assert.NotContains(t, log, "ForwardAgent=yes", "no check forwards an agent")

	out, err = f.run(t, nil)
	require.NoError(t, err, out)
	assert.Contains(t, out, "SSH-NAMES bench-1 as=root machines=3 online=2 config=same known_hosts=same include=first removed=-")
	for _, m := range []string{"bench-1", "mac-1"} {
		assert.Regexp(t, m+` +: ok=\d+ +changed=0 `, out, "a second run changed something")
	}
}

// A worker (the fleet login on a Linux machine) that gets in as root anywhere fails the run, naming the machine.
func TestSSHNamesFailsWhenAWorkerGetsIn(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	out, err := f.run(t, []string{"FAKE_SSH_WORKER_IN=1"})
	require.Error(t, err, out)
	assert.Contains(t, out, "SSH-NAMES WORKERS bench-1 denied=0 got-in=mac-1 unproven=-")
	assert.Contains(t, out, "SSH-NAMES WORKERS bench-1 FAILED")
}

// A machine the control node cannot reach with its own key fails the run with ssh's own line.
func TestSSHNamesControlFailureFailsTheRun(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	out, err := f.run(t, []string{"FAKE_SSH_FAIL=bench-1." + sshNamesTailnet})
	require.Error(t, err, out)
	assert.Contains(t, out, "SSH-NAMES CONTROL ok=1 failed=bench-1 offline=laptop-1")
	assert.Contains(t, out, "failed bench-1: ssh: connect to host bench-1.tail0test.ts.net port 22: Connection timed out")
}

// --check writes nothing, removes nothing and checks nothing, and takes -l.
func TestSSHNamesCheckWritesNothing(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	out, err := f.run(t, nil, "--check")
	require.NoError(t, err, out)
	assert.Contains(t, out, "SSH-NAMES bench-1 as=root machines=3 online=2 config=would-be-written known_hosts=would-be-written include=would-be-set removed=- (check: nothing changed)")
	assert.NotContains(t, out, "SSH-NAMES CONTROL")
	assert.NotContains(t, out, "SSH-NAMES WORKERS")
	out, err = f.run(t, nil, "--check", "-l", "bench-1")
	require.NoError(t, err, out)
	assert.Contains(t, out, "SSH-NAMES bench-1 as=root machines=3 online=1 config=would-be-written")
	f.wroteNothing(t)
}

// A write limited with -l is refused: every machine's known hosts file holds every machine's key, so a write runs on all.
func TestSSHNamesRefusesALimitedWrite(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	out, err := f.run(t, nil, "-l", "bench-1")
	require.Error(t, err, out)
	assert.Contains(t, out, "SSH-NAMES REFUSED -l limits this run to 1 of 3 machines")
	f.wroteNothing(t)
}

// A record whose address is not a tailnet name or an IP, whose user is not a login name, or whose os is neither linux
// nor darwin is refused before anything is written, naming the machine and the field, the asleep machine's included.
func TestSSHNamesRefusesABadRecord(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, func(h *sshNamesHost) {
		switch h.name {
		case "mac-1":
			h.address = "mac-1." + sshNamesTailnet + "\n  ProxyCommand sh -c id"
		case "bench-1":
			h.user = "alice bob"
		case "laptop-1":
			h.address, h.os = "laptop-1.example.com", "windows"
		}
	})
	out, err := f.run(t, nil)
	require.Error(t, err, out)
	for _, want := range []string{"SSH-NAMES REFUSED", "machine bench-1 user", "machine laptop-1 address", "machine laptop-1 os", "machine mac-1 address"} {
		assert.Contains(t, out, want)
	}
	f.wroteNothing(t)
}

// A machine name nova-config would not take ([a-z0-9][a-z0-9-]*: a leading dash would reach ssh as an option) is refused
// too, though the machine is asleep and its address and user are good.
func TestSSHNamesRefusesABadName(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	inv := readFile(t, f.inventory) + `    "-bench-2": {ansible_host: bench-2.` + sshNamesTailnet + `, ansible_user: dave, nova_os: linux, ansible_ssh_executable: "false"}` + "\n"
	require.NoError(t, os.WriteFile(f.inventory, []byte(inv), 0o644))
	out, err := f.run(t, nil)
	require.Error(t, err, out)
	assert.Contains(t, out, `SSH-NAMES REFUSED machine "-bench-2" name`)
	f.wroteNothing(t)
}

// A machine that answers but has no sshd ed25519 host key is refused before anything is written: its pin cannot be made.
func TestSSHNamesRefusesAMachineWithoutAHostKey(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	require.NoError(t, os.Remove(filepath.Join(f.dir, "key-mac-1.pub")))
	out, err := f.run(t, nil)
	require.Error(t, err, out)
	assert.Contains(t, out, "SSH-NAMES REFUSED machine mac-1 host key: none read from")
	f.wroteNothing(t)
}

// An address may be an IP.
func TestSSHNamesTakesAnIPAddress(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, func(h *sshNamesHost) {
		if h.name == "mac-1" {
			h.address = "100.64.0.3"
		}
	})
	out, err := f.run(t, nil)
	require.NoError(t, err, out)
	assert.Contains(t, readFile(t, filepath.Join(f.root["bench-1"], ".ssh", "config.d", "nova-fleet")), "Host mac-1 100.64.0.3\n  HostName 100.64.0.3\n")
	assert.Contains(t, readFile(t, filepath.Join(f.root["bench-1"], ".ssh", "nova-fleet.known_hosts")), "mac-1,100.64.0.3 "+f.key["mac-1"]+"\n")
}

// An Include of the fleet's file inside a Host or Match block of root's config applies to that block only; the file is
// left alone and the run says so.
func TestSSHNamesLeavesAnIncludeInsideABlock(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	hand := "Host old\n  User x\n  Include ~/.ssh/config.d/nova-fleet\n"
	require.NoError(t, os.WriteFile(filepath.Join(f.root["bench-1"], ".ssh", "config"), []byte(hand), 0o600))
	out, err := f.run(t, nil)
	require.Error(t, err, out) // root's ssh then does not read the names: the config check fails until it is moved
	assert.Equal(t, hand, readFile(t, filepath.Join(f.root["bench-1"], ".ssh", "config")))
	assert.Contains(t, out, "SSH-NAMES bench-1 INCLUDE LEFT ALONE")
	assert.Contains(t, out, "include=inside-a-block")
	assert.Contains(t, out, "SSH-NAMES CONFIG bench-1 ok=0 wrong=bench-1,laptop-1,mac-1")
}
