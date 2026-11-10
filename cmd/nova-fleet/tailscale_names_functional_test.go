//go:build functional

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tailscale_names_functional_test.go runs the tailscale role's names.yml, the step join and every `nova-fleet
// tailscale` run take after the join, through ansible-core against a fake machine: the local connection with become
// off, its /etc a directory of the test's (nova_etc), its home another (nova_home), and on PATH a fake `hostname` whose
// name is a file. The tailnet's view (ts_os, ts_self, ts_dns) is what main.yml would have set. Nothing outside the
// test's directory is read or written, and nothing reaches a network.

// fakeHostname prints the name in $FAKE_HOSTNAME, or with an argument sets it and logs the call.
const fakeHostname = `#!/bin/sh
if [ $# -eq 0 ]; then cat "$FAKE_HOSTNAME"; exit 0; fi
printf '%s\n' "$1" > "$FAKE_HOSTNAME"
echo "hostname $*" >> "$FAKE_HOSTNAME_CALLS"
`

const namesPlay = `- hosts: all
  gather_facts: true
  gather_subset: [min]
  tasks:
    - ansible.builtin.set_fact:
        ts_os: "{{ names_os }}"
        ts_self: "{{ names_self }}"
        ts_dns: "{{ names_self.DNSName | default('') | regex_replace('\\.$', '') }}"
    - ansible.builtin.include_role: { name: tailscale, tasks_from: names.yml }
`

const bashPS1 = `PS1='\[\033[36m\]\u@\h\[\033[00m\]:\w\$ '`

type namesMachine struct {
	dir, etc, home, inventory, play, bin, state, calls string
}

type namesOpts struct {
	name, shell, system, wsl, tsHost, tsDNS, hostname string
	hosts, wslConf, rc                                string
	cloud                                             bool
}

func newNamesMachine(t *testing.T, o namesOpts) *namesMachine {
	t.Helper()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	if o.name == "" {
		o.name = "amd-1"
	}
	if o.shell == "" {
		o.shell = "bash"
	}
	if o.system == "" {
		o.system = "Linux"
	}
	if o.wsl == "" {
		o.wsl = "false"
	}
	if o.tsHost == "" {
		o.tsHost = o.name
	}
	if o.tsDNS == "" {
		o.tsDNS = o.tsHost + ".tail0test.ts.net."
	}
	m := &namesMachine{dir: t.TempDir()}
	m.etc, m.home, m.bin = filepath.Join(m.dir, "etc"), filepath.Join(m.dir, "home"), filepath.Join(m.dir, "bin")
	m.state, m.calls = filepath.Join(m.dir, "hostname.state"), filepath.Join(m.dir, "hostname.calls")
	for _, d := range []string{m.etc, m.home, m.bin, filepath.Join(m.dir, "tmp")} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	write := func(path, body string) {
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(m.bin, "hostname"), []byte(fakeHostname), 0o755))
	write(m.state, o.hostname+"\n")
	write(filepath.Join(m.etc, "hostname"), o.hostname+"\n")
	write(filepath.Join(m.etc, "hosts"), o.hosts)
	if o.wslConf != "" {
		write(filepath.Join(m.etc, "wsl.conf"), o.wslConf)
	}
	if o.cloud {
		require.NoError(t, os.MkdirAll(filepath.Join(m.etc, "cloud", "cloud.cfg.d"), 0o755))
	}
	if o.rc != "" {
		write(filepath.Join(m.home, map[string]string{"bash": ".bashrc", "zsh": ".zshrc"}[o.shell]), o.rc)
	}
	tsOS := "debian"
	if o.system == "Darwin" {
		tsOS = "darwin"
	}
	self, err := json.Marshal(map[string]any{"HostName": o.tsHost, "DNSName": o.tsDNS})
	if err != nil {
		t.Fatal(err)
	}
	m.inventory = filepath.Join(m.dir, "inventory.yml")
	write(m.inventory, fmt.Sprintf("all:\n  hosts:\n    %s: {ansible_connection: local, ansible_become: false, ansible_python_interpreter: auto_silent, "+
		"nova_etc: %q, nova_home: %q, nova_login_shell: %s, nova_system: %s, nova_wsl: %s, names_os: %s, names_self: %s}\n",
		o.name, m.etc, m.home, o.shell, o.system, o.wsl, tsOS, self))
	m.play = filepath.Join(m.dir, "names.yml")
	write(m.play, namesPlay)
	return m
}

func (m *namesMachine) run(t *testing.T, extra ...string) (string, error) {
	t.Helper()
	roles, err := filepath.Abs("roles")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ansible-playbook", append([]string{"-i", m.inventory, m.play}, extra...)...)
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "ANSIBLE_ROLES_PATH="+roles, "HOME="+m.home, "TMPDIR="+filepath.Join(m.dir, "tmp"),
		"ANSIBLE_LOCAL_TEMP="+filepath.Join(m.dir, "tmp"), "ANSIBLE_REMOTE_TEMP="+filepath.Join(m.dir, "tmp"),
		"PATH="+m.bin+":"+os.Getenv("PATH"), "FAKE_HOSTNAME="+m.state, "FAKE_HOSTNAME_CALLS="+m.calls)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (m *namesMachine) read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

// snapshot is every file under etc and home with its content, the fake hostname's own name included.
func (m *namesMachine) snapshot(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{"hostname.state": m.read(t, m.state)}
	for _, root := range []string{m.etc, m.home} {
		require.NoError(t, filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(m.dir, p)
			if rel == filepath.Join("home", ".ansible") {
				return fs.SkipDir // ansible's own temporary files, not the machine's
			}
			if d.IsDir() {
				files[rel+"/"] = ""
				return nil
			}
			files[rel] = m.read(t, p)
			return nil
		}))
	}
	return files
}

const b1Hosts = "127.0.0.1 localhost\n127.0.1.1\tamd-1\n127.0.1.1 b1\n::1     ip6-localhost ip6-loopback\n"
const debianBashrc = "# ~/.bashrc\ncase $- in *i*) ;; *) return;; esac\nPS1='${debian_chroot:+($debian_chroot)}\\u@\\h:\\w\\$ '\n"

// A Linux machine named b1 whose record is amd-1 (amd-3's /etc/hosts, two 127.0.1.1 lines): the run renames it, leaves
// one 127.0.1.1 line, keeps the name at boot, writes the prompt and sources it once from ~/.bashrc; the drop-in's PS1
// is the fleet's string exactly, and bash renders it as nova@amd-1:~$. A second run changes nothing.
func TestNamesRenameThenNothing(t *testing.T) {
	t.Parallel()
	m := newNamesMachine(t, namesOpts{hostname: "b1", hosts: b1Hosts, cloud: true, rc: debianBashrc})
	out, err := m.run(t)
	require.NoError(t, err, out)
	assert.Contains(t, out, "HOSTNAME amd-1 renamed os=renamed from b1 hosts=set cloud-init=set tailnet=matched prompt=set", out)

	assert.Equal(t, "amd-1\n", m.read(t, m.state), "the running hostname")
	assert.Equal(t, "hostname amd-1\n", m.read(t, m.calls))
	assert.Equal(t, "amd-1\n", m.read(t, filepath.Join(m.etc, "hostname")))
	assert.Equal(t, "127.0.0.1 localhost\n127.0.1.1\tamd-1\n::1     ip6-localhost ip6-loopback\n", m.read(t, filepath.Join(m.etc, "hosts")))
	assert.Contains(t, m.read(t, filepath.Join(m.etc, "cloud", "cloud.cfg.d", "99-nova-hostname.cfg")), "\npreserve_hostname: true\n")

	dropin := filepath.Join(m.home, ".config", "nova", "prompt.bash")
	assert.Contains(t, strings.Split(m.read(t, dropin), "\n"), bashPS1, "the drop-in's PS1 is the fleet's, exactly")
	rc := m.read(t, filepath.Join(m.home, ".bashrc"))
	assert.True(t, strings.HasPrefix(rc, debianBashrc), "the rc file's own lines are kept")
	assert.Equal(t, debianBashrc+"[ -f ~/.config/nova/prompt.bash ] && . ~/.config/nova/prompt.bash\n", rc)

	if bash, err := exec.LookPath("bash"); err == nil {
		got, err := exec.Command(bash, "--norc", "--noprofile", "-i", "-c", `. "$1"; printf '%s\n' "$PS1"`, "bash", dropin).Output()
		require.NoError(t, err)
		assert.Equal(t, bashPS1[len("PS1='"):len(bashPS1)-1]+"\n", string(got), "an interactive bash takes the fleet's PS1")
		got, err = exec.Command(bash, "--norc", "--noprofile", "-c", `PS1=unset; . "$1"; printf '%s\n' "$PS1"`, "bash", dropin).Output()
		require.NoError(t, err)
		assert.Equal(t, "unset\n", string(got), "a non-interactive shell is left alone")
	}

	before := m.snapshot(t)
	out, err = m.run(t)
	require.NoError(t, err, out)
	assert.Contains(t, out, "HOSTNAME amd-1 matched os=matched hosts=matched cloud-init=matched tailnet=matched prompt=matched", out)
	assert.Contains(t, out, "changed=0", out)
	assert.Equal(t, before, m.snapshot(t), "a second run writes nothing")
	assert.Equal(t, "hostname amd-1\n", m.read(t, m.calls), "a second run sets no hostname")
}

// --check on the machine to rename writes nothing anywhere, says what it would do, and shows the hosts diff.
func TestNamesCheckWritesNothing(t *testing.T) {
	t.Parallel()
	m := newNamesMachine(t, namesOpts{hostname: "spacegame.losangeles", name: "space", cloud: true, rc: debianBashrc,
		hosts: "127.0.0.1\tlocalhost\n127.0.1.1\tubuntu-2604-amd64-tinkerbell\n127.0.1.1\tspacegame.losangeles\tspacegame\n"})
	before := m.snapshot(t)
	out, err := m.run(t, "--check", "--diff")
	require.NoError(t, err, out)
	assert.Contains(t, out, "HOSTNAME space WOULD-RENAME os=WOULD-RENAME from spacegame.losangeles hosts=WOULD-SET cloud-init=WOULD-SET tailnet=matched prompt=WOULD-SET", out)
	assert.Contains(t, out, "-127.0.1.1\tubuntu-2604-amd64-tinkerbell", out)
	assert.Contains(t, out, "+127.0.1.1\tspace", out)
	assert.Equal(t, before, m.snapshot(t), "--check wrote something")
	_, err = os.Stat(m.calls)
	assert.True(t, os.IsNotExist(err), "--check ran hostname <name>")
}

// A machine already named, with WSL's own 127.0.1.1 line (<name>.<domain> <name>) and its prompt set: nothing changes.
// On WSL the hostname goes into /etc/wsl.conf's [network] section, the section's other keys kept, or a section added.
func TestNamesMatchedAndWSL(t *testing.T) {
	t.Parallel()
	hosts := "127.0.0.1\tlocalhost\n127.0.1.1\tcaptain.localdomain\tcaptain\n"
	rc := debianBashrc + "[ -f ~/.config/nova/prompt.bash ] && . ~/.config/nova/prompt.bash\n"
	m := newNamesMachine(t, namesOpts{name: "captain", hostname: "captain", wsl: "true", hosts: hosts, rc: rc,
		wslConf: "[boot]\nsystemd=true\n[network]\ngenerateResolvConf = false\n"})
	out, err := m.run(t)
	require.NoError(t, err, out)
	assert.Equal(t, "[boot]\nsystemd=true\n[network]\nhostname = captain\ngenerateResolvConf = false\n", m.read(t, filepath.Join(m.etc, "wsl.conf")))
	assert.Equal(t, hosts, m.read(t, filepath.Join(m.etc, "hosts")), "WSL's own line names the machine: kept")
	assert.Equal(t, rc, m.read(t, filepath.Join(m.home, ".bashrc")), "the source line is there once already")
	_, err = os.Stat(m.calls)
	assert.True(t, os.IsNotExist(err), "a matched machine is not renamed")
	assert.Contains(t, out, "cloud-init=none", out)

	out, err = m.run(t)
	require.NoError(t, err, out)
	assert.Contains(t, out, "HOSTNAME captain matched os=matched hosts=matched cloud-init=none tailnet=matched prompt=matched", out)
	assert.Contains(t, out, "changed=0", out)

	w := newNamesMachine(t, namesOpts{name: "captain", hostname: "captainamerica", wsl: "true", hosts: hosts, wslConf: "[boot]\nsystemd=true\n"})
	out, err = w.run(t)
	require.NoError(t, err, out)
	assert.Contains(t, w.read(t, filepath.Join(w.etc, "wsl.conf")), "[boot]\nsystemd=true\n# BEGIN nova-fleet hostname (roles/tailscale)\n[network]\nhostname = captain\n")
}

// A Mac keeps its hostname and /etc; its zsh login gets the fleet's prompt with the record name, from ~/.zshrc.
func TestNamesMacPromptOnly(t *testing.T) {
	t.Parallel()
	m := newNamesMachine(t, namesOpts{name: "studio", hostname: "studio.local", system: "Darwin", shell: "zsh", hosts: "127.0.0.1 localhost\n"})
	before := m.snapshot(t)
	out, err := m.run(t)
	require.NoError(t, err, out)
	assert.Contains(t, out, "HOSTNAME studio matched os=left (macOS: not renamed) hosts=left cloud-init=none tailnet=matched prompt=set", out)
	after := m.snapshot(t)
	for k, v := range before {
		if strings.HasPrefix(k, "etc") || k == "hostname.state" {
			assert.Equal(t, v, after[k], "%s changed on a Mac", k)
		}
	}
	assert.Contains(t, strings.Split(m.read(t, filepath.Join(m.home, ".config", "nova", "prompt.zsh")), "\n"), `PROMPT='%F{cyan}%n@studio%f:%~%# '`)
	assert.Equal(t, "[ -f ~/.config/nova/prompt.zsh ] && . ~/.config/nova/prompt.zsh\n", m.read(t, filepath.Join(m.home, ".zshrc")))
	if zsh, err := exec.LookPath("zsh"); err == nil {
		got, err := exec.Command(zsh, "-f", "-i", "-c", `. "$1"; print -r -- "$PROMPT"`, "zsh", filepath.Join(m.home, ".config", "nova", "prompt.zsh")).Output()
		require.NoError(t, err)
		assert.Equal(t, "%F{cyan}%n@studio%f:%~%# \n", string(got))
	}

	b := newNamesMachine(t, namesOpts{name: "mini-m5", hostname: "mini-m5", system: "Darwin", shell: "bash", hosts: "127.0.0.1 localhost\n"})
	out, err = b.run(t)
	require.NoError(t, err, out)
	assert.Contains(t, strings.Split(b.read(t, filepath.Join(b.home, ".config", "nova", "prompt.bash")), "\n"), `PS1='\[\033[36m\]\u@mini-m5\[\033[00m\]:\w\$ '`)
	assert.Equal(t, "[ -f ~/.config/nova/prompt.bash ] && . ~/.config/nova/prompt.bash\n", b.read(t, filepath.Join(b.home, ".bash_profile")))
}

// drift-refused: a tailnet that gave the machine another name (another node holds it) and a record name that is not a
// hostname both stop the machine, saying why, before anything is written.
func TestNamesDriftRefused(t *testing.T) {
	t.Parallel()
	m := newNamesMachine(t, namesOpts{hostname: "b1", hosts: b1Hosts, tsDNS: "amd-1-1.tail0test.ts.net."})
	out, err := m.run(t)
	require.Error(t, err, out)
	assert.Contains(t, out, "HOSTNAME amd-1 drift-refused: the tailnet gave this machine amd-1-1.tail0test.ts.net", out)

	bad := newNamesMachine(t, namesOpts{name: "Bad_Name", hostname: "b1", hosts: b1Hosts})
	before := bad.snapshot(t)
	out, err = bad.run(t)
	require.Error(t, err, out)
	assert.Contains(t, out, "HOSTNAME Bad_Name drift-refused: the record name is not a hostname", out)
	assert.Equal(t, before, bad.snapshot(t))
}
