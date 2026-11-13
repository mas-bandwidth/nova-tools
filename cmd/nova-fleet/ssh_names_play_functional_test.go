//go:build functional

package main

import (
	"context"
	"encoding/json"
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
// machines reached with the local connection, each with its own home (nova_home) and its own host key file
// (ssh_names_host_key), and a laptop whose ssh is `false`, so it never answers. On PATH: a fake ssh for the checks that
// logs its arguments and answers as the fleet should (see fakeSSH), and a fake tailscale whose `status --json` is the
// file FAKE_TS_STATUS names.

// fakeSSH logs its arguments. A target named in $FAKE_SSH_OPEN lets the login in, one in $FAKE_SSH_TIMEOUT times out,
// one in $FAKE_SSH_POLICY is refused by Tailscale SSH's policy, one in $FAKE_SSH_HOSTKEY presents another host key,
// one in $FAKE_SSH_LOCAL fails on a local file with a "Permission denied" of its own (the lists are blank-separated
// names or addresses). Otherwise the control node's login (-l) gets in, and a machine's login is refused by sshd.
const fakeSSH = `#!/bin/sh
echo "$*" >> "$FAKE_SSH_LOG"
control=
for a in "$@"; do
  [ "$a" = "-l" ] && control=1
  case " $FAKE_SSH_OPEN " in *" $a "*) exit 0;; esac
  case " $FAKE_SSH_HOSTKEY " in *" $a "*) printf '%s\n' "@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@" "@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!     @" "Host key for $a has changed and you have requested strict checking." "Host key verification failed." >&2; exit 255;; esac
  case " $FAKE_SSH_LOCAL " in *" $a "*) echo "Load key \"/home/alice/.ssh/id_ed25519\": Permission denied" >&2; echo "ssh: connect to host $a port 22: Connection refused" >&2; exit 255;; esac
  case " $FAKE_SSH_TIMEOUT " in *" $a "*) echo "ssh: connect to host $a port 22: Operation timed out" >&2; exit 255;; esac
  case " $FAKE_SSH_POLICY " in *" $a "*) echo "tailscale: tailnet policy does not permit you to SSH to this node" >&2; exit 255;; esac
done
[ -n "$control" ] && exit 0
echo "alice@host: Permission denied (publickey)." >&2
exit 255
`

// fakeTailscale answers `status --json` with $FAKE_TS_STATUS, as the control node's tailscale would.
const fakeTailscale = `#!/bin/sh
[ "$1 $2" = "status --json" ] && [ -f "$FAKE_TS_STATUS" ] && exec cat "$FAKE_TS_STATUS"
echo "tailscale: failed to connect to local tailscaled" >&2
exit 1
`

const sshNamesTailnet = "tail0test.ts.net"

type sshNamesHost struct{ name, address, user string }

type sshNamesFleet struct {
	dir, inventory, bin, sshLog, tsStatus string
	home, key                             map[string]string
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
	f := &sshNamesFleet{dir: t.TempDir(), home: map[string]string{}, key: map[string]string{}}
	f.bin = filepath.Join(f.dir, "bin")
	f.sshLog = filepath.Join(f.dir, "ssh.log")
	f.tsStatus = filepath.Join(f.dir, "tailscale-status.json")
	require.NoError(t, os.MkdirAll(f.bin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.bin, "ssh"), []byte(fakeSSH), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.bin, "tailscale"), []byte(fakeTailscale), 0o755))
	hosts := []sshNamesHost{{"bench-1", "", "alice"}, {"laptop-1", "", "carol"}, {"mac-1", "", "bob"}}
	var inv strings.Builder
	inv.WriteString("all:\n  hosts:\n")
	for _, h := range hosts {
		h.address = h.name + "." + sshNamesTailnet
		f.key[h.name] = sshNamesKey(h.name)
		if edit != nil {
			edit(&h)
		}
		if h.name == "laptop-1" {
			fmt.Fprintf(&inv, "    laptop-1: {ansible_host: %q, ansible_user: %q, ansible_ssh_executable: %q}\n", h.address, h.user, never)
			continue
		}
		f.home[h.name] = filepath.Join(f.dir, "home-"+h.name)
		require.NoError(t, os.MkdirAll(filepath.Join(f.home[h.name], ".ssh"), 0o700))
		keyFile := filepath.Join(f.dir, "key-"+h.name+".pub")
		require.NoError(t, os.WriteFile(keyFile, []byte(f.key[h.name]+" root@"+h.name+"\n"), 0o644))
		fmt.Fprintf(&inv, "    %s: {ansible_connection: local, ansible_python_interpreter: auto_silent, ansible_host: %q, ansible_user: %q, nova_home: %q, ssh_names_host_key: %q}\n",
			h.name, h.address, h.user, f.home[h.name], keyFile)
	}
	f.inventory = filepath.Join(f.dir, "inventory.yml")
	require.NoError(t, os.WriteFile(f.inventory, []byte(inv.String()), 0o644))
	f.tailnet(t, nil, nil)
	return f
}

// tailnet writes the control node's `tailscale status --json`: every machine a peer tagged tag:nova that publishes no
// host key (Tailscale SSH off, as the fleet runs), except where published (a machine's published keys: it still serves
// Tailscale SSH) or tags say otherwise.
func (f *sshNamesFleet) tailnet(t *testing.T, published map[string][]string, tags map[string][]string) {
	t.Helper()
	peers := map[string]any{}
	for i, h := range []string{"bench-1", "laptop-1", "mac-1"} {
		keys := published[h]
		tg, ok := tags[h]
		if !ok {
			tg = []string{"tag:nova"}
		}
		peers["nodekey:"+h] = map[string]any{"HostName": h, "DNSName": h + "." + sshNamesTailnet + ".",
			"TailscaleIPs": []string{fmt.Sprintf("100.64.0.%d", i+1)}, "Tags": tg, "sshHostKeys": keys}
	}
	b, err := json.Marshal(map[string]any{"BackendState": "Running", "MagicDNSSuffix": sshNamesTailnet,
		"Self": map[string]any{"HostName": "control", "DNSName": "control." + sshNamesTailnet + ".", "TailscaleIPs": []string{"100.64.0.9"}},
		"Peer": peers})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.tsStatus, b, 0o644))
}

// run runs the play; fake is fakeSSH's environment, as "FAKE_SSH_OPEN=mac-1", or "".
func (f *sshNamesFleet) run(t *testing.T, fake string, extra ...string) (string, error) {
	t.Helper()
	play, err := filepath.Abs("ssh-names.yml")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ansible-playbook", append([]string{"-i", f.inventory, play}, extra...)...)
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "HOME="+f.dir, "TMPDIR="+f.dir,
		"ANSIBLE_LOCAL_TEMP="+filepath.Join(f.dir, "tmp"), "PATH="+f.bin+":"+os.Getenv("PATH"),
		"FAKE_SSH_OPEN=", "FAKE_SSH_TIMEOUT=", "FAKE_SSH_POLICY=", "FAKE_SSH_HOSTKEY=", "FAKE_SSH_LOCAL=", "FAKE_SSH_LOG="+f.sshLog, "FAKE_TS_STATUS="+f.tsStatus)
	if fake != "" {
		cmd.Env = append(cmd.Env, fake)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (f *sshNamesFleet) read(t *testing.T, machine, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.home[machine], rel))
	require.NoError(t, err)
	return string(b)
}

// sshG is real ssh's view of a name from a machine's ~/.ssh/config, read without the network: `ssh -G -F` over a copy
// whose ~/ is that machine's home (ssh takes ~ from the password database, never $HOME), so the Include lines reach the
// files the play wrote.
func (f *sshNamesFleet) sshG(t *testing.T, machine, name string) map[string]string {
	t.Helper()
	ssh, err := exec.LookPath("ssh")
	require.NoError(t, err, "ssh -G checks the files the play writes; the client must be installed")
	resolved := filepath.Join(f.dir, "resolved-"+machine)
	conf := strings.ReplaceAll(f.read(t, machine, ".ssh/config"), "~/", f.home[machine]+"/")
	require.NoError(t, os.WriteFile(resolved, []byte(conf), 0o600))
	out, err := exec.Command(ssh, "-G", "-F", resolved, name).CombinedOutput()
	require.NoError(t, err, "ssh -G %s from %s:\n%s", name, machine, out)
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
	for _, m := range []string{"bench-1", "mac-1"} {
		entries, err := os.ReadDir(filepath.Join(f.home[m], ".ssh"))
		require.NoError(t, err)
		assert.Empty(t, entries, "something was written into %s's ~/.ssh", m)
	}
}

const wantSSHNamesConfig = `# nova-fleet ssh-names: one block per machine of the fleet record, written by ` + "`nova-fleet ssh-names`" + `; a run rewrites it.
# ~/.ssh/config includes this file on its first line, so for these names the HostName and User here win over a Host
# block of the same name there, and that block's other options still apply.

Host bench-1 bench-1.tail0test.ts.net
  HostName bench-1.tail0test.ts.net
  User alice
  UserKnownHostsFile ~/.ssh/known_hosts ~/.ssh/nova-fleet.known_hosts

Host laptop-1 laptop-1.tail0test.ts.net
  HostName laptop-1.tail0test.ts.net
  User carol
  UserKnownHostsFile ~/.ssh/known_hosts ~/.ssh/nova-fleet.known_hosts

Host mac-1 mac-1.tail0test.ts.net
  HostName mac-1.tail0test.ts.net
  User bob
  UserKnownHostsFile ~/.ssh/known_hosts ~/.ssh/nova-fleet.known_hosts
`

// Every machine that answers gets one block per machine of the inventory (the laptop asleep included) and the Include
// first in its ssh config: moved there from below a top-level option (a CRLF line, not duplicated), written through a
// symlinked config, and read by real ssh even where the login includes all of ~/.ssh/config.d. The known hosts file
// holds the key each machine that answered read from itself and the kept line of the one that did not; the mesh pins
// host keys to it; the laptop is reported offline and the run passes; a second run changes nothing.
func TestSSHNamesWritesEveryMachineAndSkipsTheOneAsleep(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	tail := "Include ~/.ssh/config.d/*\n\nHost mac-1\n  HostName 192.0.2.5\n  User root\n  IdentityFile ~/.ssh/old\n"
	hand := "IdentitiesOnly yes\r\nInclude ~/.ssh/config.d/nova-fleet\r\n" + tail
	require.NoError(t, os.WriteFile(filepath.Join(f.home["bench-1"], ".ssh", "config"), []byte(hand), 0o644))
	kept := "laptop-1,laptop-1.tail0test.ts.net " + f.key["laptop-1"]
	require.NoError(t, os.WriteFile(filepath.Join(f.home["bench-1"], ".ssh", "nova-fleet.known_hosts"),
		[]byte(kept+"\nmac-1,mac-1.tail0test.ts.net ssh-ed25519 AAAAstale\n"), 0o600))
	dotfile := filepath.Join(f.home["mac-1"], "dotfiles", "ssh_config")
	require.NoError(t, os.MkdirAll(filepath.Dir(dotfile), 0o700))
	require.NoError(t, os.WriteFile(dotfile, []byte("Host *\n  ServerAliveInterval 30\n"), 0o600))
	require.NoError(t, os.Symlink(dotfile, filepath.Join(f.home["mac-1"], ".ssh", "config")))

	out, err := f.run(t, "")
	require.NoError(t, err, out)
	assert.Contains(t, out, "SSH-NAMES laptop-1 OFFLINE")
	assert.Contains(t, out, "SSH-NAMES CONTROL ok=2 failed=- tailscale-ssh=- offline=laptop-1")
	for _, m := range []string{"bench-1", "mac-1"} {
		assert.Contains(t, out, "SSH-NAMES MESH "+m+" refused=1 open=- hostkey=- timeout=- unreached=- offline=laptop-1")
		assert.Equal(t, wantSSHNamesConfig, f.read(t, m, ".ssh/config.d/nova-fleet"), m)
		entries, err := os.ReadDir(filepath.Join(f.home[m], ".ssh", "config.d"))
		require.NoError(t, err)
		require.Len(t, entries, 1, "config.d holds the config only: a login that includes config.d/* reads every file there as config")
		for name, user := range map[string]string{"bench-1": "alice", "mac-1": "bob", "laptop-1": "carol"} {
			g := f.sshG(t, m, name)
			assert.Equal(t, name+"."+sshNamesTailnet, g["hostname"], "ssh -G %s from %s", name, m)
			assert.Equal(t, user, g["user"], "ssh -G %s from %s: the record's user wins over a hand-written block", name, m)
		}
	}
	assert.Equal(t, "Include ~/.ssh/config.d/nova-fleet\nIdentitiesOnly yes\r\n"+tail, f.read(t, "bench-1", ".ssh/config"),
		"the Include first, once; the rest kept")
	link, err := os.Readlink(filepath.Join(f.home["mac-1"], ".ssh", "config"))
	require.NoError(t, err, "a symlinked ~/.ssh/config stays a symlink")
	assert.Equal(t, dotfile, link)
	assert.Equal(t, "Include ~/.ssh/config.d/nova-fleet\nHost *\n  ServerAliveInterval 30\n", f.read(t, "mac-1", "dotfiles/ssh_config"))

	assert.Equal(t, "bench-1,bench-1.tail0test.ts.net "+f.key["bench-1"]+"\n"+kept+"\nmac-1,mac-1.tail0test.ts.net "+f.key["mac-1"]+"\n",
		f.read(t, "bench-1", ".ssh/nova-fleet.known_hosts"), "each key read again, the asleep machine's kept, the stale one gone")
	assert.Equal(t, "bench-1,bench-1.tail0test.ts.net "+f.key["bench-1"]+"\nmac-1,mac-1.tail0test.ts.net "+f.key["mac-1"]+"\n",
		f.read(t, "mac-1", ".ssh/nova-fleet.known_hosts"), "no line is made up for a machine never seen")
	log, err := os.ReadFile(f.sshLog)
	require.NoError(t, err)
	assert.Contains(t, string(log), "-o StrictHostKeyChecking=yes -o UserKnownHostsFile="+f.home["bench-1"]+"/.ssh/nova-fleet.known_hosts mac-1 true")
	assert.Contains(t, string(log), "-n -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=yes -l bob mac-1.tail0test.ts.net true",
		"the control node logs in as the record's login at the record's address")
	assert.NotContains(t, string(log), "laptop-1", "a machine asleep is tried by no one")

	out, err = f.run(t, "")
	require.NoError(t, err, out)
	assert.Contains(t, out, "SSH-NAMES bench-1 machines=3 online=2 config=same known_hosts=same include=first unchecked=bench-1,mac-1")
	for _, m := range []string{"bench-1", "mac-1"} {
		assert.Regexp(t, m+` +: ok=\d+ +changed=0 `, out, "a second run changed something")
	}
}

// A login from one machine to another fails the run, naming the pair; the other machine's refused pair passes.
func TestFunctionalSSHNamesMachineToMachineLoginFailsTheRun(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	out, err := f.run(t, "FAKE_SSH_OPEN=mac-1")
	require.Error(t, err, out)
	assert.Contains(t, out, "SSH-NAMES MESH bench-1 refused=0 open=bench-1->mac-1 hostkey=- timeout=- unreached=- offline=laptop-1")
	assert.Contains(t, out, "SSH-NAMES MESH bench-1 OPEN bench-1->mac-1: this machine's login logged in there")
	assert.Contains(t, out, "SSH-NAMES MESH mac-1 refused=1 open=- hostkey=- timeout=- unreached=- offline=laptop-1")
}

// Refused by Tailscale SSH's policy counts as refused; a pair that times out or fails another way is reported and
// passes: it is no login.
func TestFunctionalSSHNamesTimeoutsAndPolicyRefusalsPass(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	out, err := f.run(t, "FAKE_SSH_TIMEOUT=mac-1")
	require.NoError(t, err, out)
	assert.Contains(t, out, "SSH-NAMES MESH bench-1 refused=0 open=- hostkey=- timeout=mac-1 unreached=- offline=laptop-1")
	assert.Contains(t, out, "SSH-NAMES MESH mac-1 refused=1 open=- hostkey=- timeout=- unreached=- offline=laptop-1")
	out, err = f.run(t, "FAKE_SSH_POLICY=bench-1")
	require.NoError(t, err, out)
	assert.Contains(t, out, "SSH-NAMES MESH mac-1 refused=1 open=- hostkey=- timeout=- unreached=- offline=laptop-1")
}

// The control node runs no mesh (it is the one way in) and is a target like any other machine, to be refused; the
// control node's own login check covers every machine but itself.
func TestFunctionalSSHNamesControlNodeIsTheWayIn(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	f.tailnet(t, nil, map[string][]string{"mac-1": {"tag:control"}})
	out, err := f.run(t, "")
	require.NoError(t, err, out)
	assert.Contains(t, out, "SSH-NAMES MESH bench-1 refused=1 open=- hostkey=- timeout=- unreached=- offline=laptop-1")
	assert.NotContains(t, out, "SSH-NAMES MESH mac-1")
	assert.Contains(t, out, "SSH-NAMES CONTROL ok=1 failed=- tailscale-ssh=- offline=laptop-1")
	log, err := os.ReadFile(f.sshLog)
	require.NoError(t, err)
	assert.NotContains(t, string(log), "-l bob", "the control node is not checked into itself")
}

// A machine the control node cannot log in to by key fails the run, naming it with ssh's own line.
func TestFunctionalSSHNamesControlLoginFailureFailsTheRun(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	out, err := f.run(t, "FAKE_SSH_TIMEOUT=bench-1."+sshNamesTailnet)
	require.Error(t, err, out)
	assert.Contains(t, out, "SSH-NAMES CONTROL ok=1 failed=bench-1 tailscale-ssh=- offline=laptop-1")
	assert.Contains(t, out, "SSH-NAMES CONTROL FAILED failed bench-1: ssh: connect to host bench-1."+sshNamesTailnet+" port 22: Operation timed out")
}

// --check writes nothing and checks no pair, and takes -l.
func TestSSHNamesCheckWritesNothing(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	out, err := f.run(t, "", "--check")
	require.NoError(t, err, out)
	assert.Contains(t, out, "SSH-NAMES bench-1 machines=3 online=2 config=would-be-written known_hosts=would-be-written include=would-be-set unchecked=bench-1,mac-1 (check: nothing changed)")
	assert.NotContains(t, out, "SSH-NAMES MESH")
	assert.NotContains(t, out, "SSH-NAMES CONTROL")
	out, err = f.run(t, "", "--check", "-l", "bench-1")
	require.NoError(t, err, out)
	assert.Contains(t, out, "SSH-NAMES bench-1 machines=3 online=1 config=would-be-written")
	f.wroteNothing(t)
}

// A write limited with -l is refused: every machine's known hosts file holds every machine's key, so a write runs on all.
func TestSSHNamesRefusesALimitedWrite(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	out, err := f.run(t, "", "-l", "bench-1")
	require.Error(t, err, out)
	assert.Contains(t, out, "SSH-NAMES REFUSED -l limits this run to 1 of 3 machines")
	f.wroteNothing(t)
}

// A record whose address is not a name on this tailnet or an IP, or whose user is not a login name, is refused before
// anything is written, naming the machine and the field, the asleep machine's record included.
func TestSSHNamesRefusesABadRecord(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, func(h *sshNamesHost) {
		switch h.name {
		case "mac-1":
			h.address = "mac-1." + sshNamesTailnet + "\n  ProxyCommand sh -c id"
		case "bench-1":
			h.user = "alice bob"
		case "laptop-1":
			h.address = "laptop-1.other.ts.net"
		}
	})
	out, err := f.run(t, "")
	require.Error(t, err, out)
	for _, want := range []string{"SSH-NAMES REFUSED", "machine bench-1 user", "machine laptop-1 address", "machine mac-1 address"} {
		assert.Contains(t, out, want)
	}
	f.wroteNothing(t)
}

// A machine name nova-config would not take ([a-z0-9][a-z0-9-]*: a leading dash would reach ssh as an option) is refused
// too, though the machine is asleep and its address and user are good.
func TestSSHNamesRefusesABadName(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	inv, err := os.ReadFile(f.inventory)
	require.NoError(t, err)
	inv = append(inv, []byte(`    "-bench-2": {ansible_host: bench-2.`+sshNamesTailnet+`, ansible_user: dave, ansible_ssh_executable: "false"}`+"\n")...)
	require.NoError(t, os.WriteFile(f.inventory, inv, 0o644))
	out, err := f.run(t, "")
	require.Error(t, err, out)
	assert.Contains(t, out, `SSH-NAMES REFUSED machine "-bench-2" name`)
	f.wroteNothing(t)
}

// An address may be one of the tailnet's IPs.
func TestSSHNamesTakesAnIPAddress(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, func(h *sshNamesHost) {
		if h.name == "mac-1" {
			h.address = "100.64.0.3"
		}
	})
	out, err := f.run(t, "")
	require.NoError(t, err, out)
	assert.Contains(t, f.read(t, "bench-1", ".ssh/config.d/nova-fleet"), "Host mac-1 100.64.0.3\n  HostName 100.64.0.3\n")
	assert.Contains(t, f.read(t, "bench-1", ".ssh/nova-fleet.known_hosts"), "mac-1,100.64.0.3 "+f.key["mac-1"]+"\n")
}

// A host key read from a machine that differs from the one the tailnet publishes for it is refused before anything is
// written; a machine that publishes none (it does not serve Tailscale SSH) is written and reported unchecked.
func TestSSHNamesCrossChecksHostKeysWithTheTailnet(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	f.tailnet(t, map[string][]string{"mac-1": {"ssh-rsa AAAAB3rsa", sshNamesKey("someone-else")}}, nil)
	out, err := f.run(t, "")
	require.Error(t, err, out)
	assert.Contains(t, out, "SSH-NAMES REFUSED machine mac-1 host key")
	f.wroteNothing(t)

	// mac-1 publishes its own key (it still serves Tailscale SSH): written, checked; bench-1 publishes none: unchecked.
	// The control node's login into mac-1 would prove no key, so the run fails on it, naming it.
	f.tailnet(t, map[string][]string{"mac-1": {sshNamesKey("mac-1")}}, nil)
	out, err = f.run(t, "")
	require.Error(t, err, out)
	assert.Contains(t, out, "SSH-NAMES mac-1 machines=3 online=2 config=written known_hosts=written include=set unchecked=bench-1")
	assert.Contains(t, f.read(t, "mac-1", ".ssh/nova-fleet.known_hosts"), "bench-1,bench-1.tail0test.ts.net "+f.key["bench-1"]+"\n")
	assert.Contains(t, out, "SSH-NAMES CONTROL ok=1 failed=- tailscale-ssh=mac-1 offline=laptop-1")
	assert.Contains(t, out, "SSH-NAMES CONTROL FAILED tailscale-ssh mac-1: Tailscale SSH still on (it lets in a node, not a key)")
	log, err := os.ReadFile(f.sshLog)
	require.NoError(t, err)
	assert.NotContains(t, string(log), "-l bob", "a machine still serving Tailscale SSH is not taken as a key login")
}

// A target answering with another host key fails the run (a machine answering for another is no refusal); a local
// "Permission denied" (on a key file) is no refusal by the target either: it is reported unreached.
func TestFunctionalSSHNamesHostKeyMismatchFailsAndLocalErrorsAreNoRefusal(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	out, err := f.run(t, "FAKE_SSH_HOSTKEY=mac-1")
	require.Error(t, err, out)
	assert.Contains(t, out, "SSH-NAMES MESH bench-1 refused=0 open=- hostkey=mac-1 timeout=- unreached=- offline=laptop-1")
	assert.Contains(t, out, "SSH-NAMES MESH bench-1 HOSTKEY hostkey mac-1: Host key verification failed.")
	assert.Contains(t, out, "SSH-NAMES MESH mac-1 refused=1 open=- hostkey=- timeout=- unreached=- offline=laptop-1")

	out, err = f.run(t, "FAKE_SSH_LOCAL=mac-1")
	require.NoError(t, err, out)
	assert.Contains(t, out, "SSH-NAMES MESH bench-1 refused=0 open=- hostkey=- timeout=- unreached=mac-1 offline=laptop-1")
}

// A run that cannot read the tailnet on the control node is refused before anything is written.
func TestSSHNamesRefusesWithoutTheTailnet(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	require.NoError(t, os.Remove(f.tsStatus))
	out, err := f.run(t, "")
	require.Error(t, err, out)
	assert.Contains(t, out, "SSH-NAMES REFUSED the tailnet cannot be read on the control node")
	f.wroteNothing(t)
}

// An Include of the fleet's file inside a Host or Match block applies to that block only; the file is left alone and
// the run says so.
func TestSSHNamesLeavesAnIncludeInsideABlock(t *testing.T) {
	t.Parallel()
	f := newSSHNamesFleet(t, nil)
	hand := "Host old\n  User x\n  Include ~/.ssh/config.d/nova-fleet\n"
	require.NoError(t, os.WriteFile(filepath.Join(f.home["bench-1"], ".ssh", "config"), []byte(hand), 0o600))
	out, err := f.run(t, "")
	require.NoError(t, err, out)
	assert.Equal(t, hand, f.read(t, "bench-1", ".ssh/config"))
	assert.Contains(t, out, "SSH-NAMES bench-1 INCLUDE LEFT ALONE")
	assert.Contains(t, out, "SSH-NAMES bench-1 machines=3 online=2 config=written known_hosts=written include=inside-a-block")
}
