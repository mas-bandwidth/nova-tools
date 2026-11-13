package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestJoinLoginPlayBecomesRootThroughAFirstLogin: join's login play connects as nova_join_root_user, becomes root
// only when that login is not root, and gives the fleet's login exactly the first login's own keys (a cloud image's
// root keys may only print "log in as ubuntu") and the control node's key, which the first play reads before it
// writes the row.
func TestJoinLoginPlayBecomesRootThroughAFirstLogin(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("join.yml")
	require.NoError(t, err)
	var plays []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &plays))
	var login, row map[string]any
	for _, p := range plays {
		switch asText(p["name"]) {
		case "the login (Linux, as root, or as the first login through sudo)":
			login = p
		case "the machine row (control node)":
			row = p
		}
	}
	require.NotNil(t, login, "join.yml has no login play")
	require.NotNil(t, row, "join.yml has no machine row play")
	rowTasks := flattenTasks(t, toTasks(row["tasks"]))
	ck, add := taskIndex(rowTasks, "the control node's public key"), taskIndex(rowTasks, "the machine row added")
	require.True(t, ck >= 0 && add > ck, "the control node's key is read before the row is written")
	assert.Equal(t, "controlkey", asText(rowTasks[ck]["ansible.builtin.include_role"].(map[string]any)["name"]))

	assert.Equal(t, "{{ nova_join_root_user != 'root' }}", asText(login["become"]))
	assert.Equal(t, "{{ nova_join_root_user }}", asText(login["vars"].(map[string]any)["ansible_user"]))
	tasks := flattenTasks(t, toTasks(login["tasks"]))
	home := taskIndex(tasks, "the home of the first login")
	read := taskIndex(tasks, "the authorized keys of the first login")
	keys := taskIndex(tasks, "the authorized keys of {{ join_login }}")
	require.True(t, home >= 0 && read > home && keys > read, "the first login's home is read, then its keys, before the login's are written")
	src := asText(tasks[read]["ansible.builtin.slurp"].(map[string]any)["src"])
	assert.Contains(t, src, "getent_passwd'][nova_join_root_user][4] ~ '/.ssh/authorized_keys'")
	assert.NotContains(t, src, "/root/.ssh")
	ak := tasks[keys]["ansible.posix.authorized_key"].(map[string]any)
	assert.Equal(t, true, ak["exclusive"], "exactly these keys: a second run changes nothing, and no other key stays")
	assert.Contains(t, asText(ak["key"]), "~ '\n' ~ hostvars['localhost']['nova_control_key']")
}

// TestTailscaleSSHIsOffAndTheControlNodeKeepsAWayIn: Tailscale SSH is off by default (it lets in a source node, so a
// CI job on one machine could log in to another and sudo there); a join brings a machine up without --ssh, and on a
// machine that serves it the role first checks sshd on the node's tailnet address and authorizes the control node's
// key, then, as its very last step and detached from the run's own connection, turns it off behind a dead-man switch
// that only a real key login over a fresh connection disarms. Nothing earlier ever passes --ssh=false.
func TestTailscaleSSHIsOffAndTheControlNodeKeepsAWayIn(t *testing.T) {
	t.Parallel()
	d, err := os.ReadFile("roles/tailscale/defaults/main.yml")
	require.NoError(t, err)
	var defaults map[string]any
	require.NoError(t, yaml.Unmarshal(d, &defaults))
	assert.Equal(t, false, defaults["nova_tailscale_ssh"])

	b, err := os.ReadFile("roles/tailscale/tasks/main.yml")
	require.NoError(t, err)
	var top []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &top))
	last := top[len(top)-1]
	assert.Equal(t, "ssh-off.yml", asText(last["ansible.builtin.include_tasks"]), "the switch is the role's last step")
	assert.NotContains(t, string(b), "--ssh=false", "nothing in the role's main tasks turns Tailscale SSH off in-line")

	tasks := roleFileTasks(t, "roles/tailscale/tasks/main.yml")
	var switches int
	for _, task := range tasks {
		for _, k := range []string{"ansible.builtin.include_tasks", "ansible.builtin.import_tasks", "include_tasks", "import_tasks"} {
			if strings.Contains(asText(task[k]), "ssh-off") {
				switches++
			}
		}
	}
	assert.Equal(t, 1, switches, "the switch runs once, as the role's last step")
	what := taskIndex(tasks, "what the join must do")
	require.GreaterOrEqual(t, what, 0)
	assert.Contains(t, asText(tasks[what]["ansible.builtin.set_fact"].(map[string]any)["ts_ssh_flag"]), "nova_tailscale_ssh | bool")
	var order []int
	for _, name := range []string{"whether this run turns Tailscale SSH off", "the control node's public key", "the control node on the tailnet",
		"sshd on this machine's tailnet address", "sshd's settings for this login", "sshd takes this login by key",
		"the control node's key authorized for", "tailscale set (hostname, ssh)", "the receipt", "Tailscale SSH off (the last step)"} {
		i := taskIndex(tasks, name)
		require.GreaterOrEqual(t, i, 0, "no task %q", name)
		order = append(order, i)
	}
	assert.IsIncreasing(t, order, "sshd checked and the key authorized first; the switch after everything else")
	off := asText(tasks[order[0]]["ansible.builtin.set_fact"].(map[string]any)["ts_ssh_off"])
	assert.Contains(t, off, "RunSSH")
	assert.Contains(t, off, "not (nova_tailscale_ssh | bool)")
	for _, i := range order[1:7] {
		assert.True(t, strings.HasPrefix(asText(tasks[i]["when"]), "ts_ssh_off"), "task %q", tasks[i]["name"])
	}
	// an earlier run's switch, still deciding, is waited out before Tailscale SSH's state is read
	pending := taskIndex(tasks, "no earlier switch still pending")
	require.GreaterOrEqual(t, pending, 0)
	assert.Less(t, pending, taskIndex(tasks, "the running node's preferences"))
	wait := tasks[pending]["ansible.builtin.wait_for"].(map[string]any)
	assert.Equal(t, "{{ nova_tailscale_ssh_pending }}", asText(wait["path"]))
	assert.Equal(t, "absent", asText(wait["state"]))
	// sshd probed on the machine, at its tailnet address, by OpenSSH's banner: over the tailnet, Tailscale SSH (still on
	// here) answers port 22 itself
	probe := tasks[order[3]]["ansible.builtin.wait_for"].(map[string]any)
	assert.Equal(t, "{{ ts_tail_ip }}", asText(probe["host"]), "the probe is the node's tailnet address")
	assert.Equal(t, 22, probe["port"])
	assert.Equal(t, `^SSH-2\.0-OpenSSH`, asText(probe["search_regex"]), "a connection alone may be Tailscale SSH's")
	assert.Nil(t, tasks[order[3]]["delegate_to"], "from the machine: over the tailnet Tailscale SSH answers, not sshd")
	assert.Contains(t, strings.Join(asStrings(tasks[order[4]]["ansible.builtin.command"].(map[string]any)["argv"]), " "), "-T -C user={{ ansible_facts['user_id'] }},host=")
	assert.Equal(t, "{{ nova_control_key }}", asText(tasks[order[6]]["ansible.posix.authorized_key"].(map[string]any)["key"]))
	set := asText(tasks[order[7]]["ansible.builtin.command"].(map[string]any)["argv"])
	assert.Contains(t, set, "['--ssh=true'] if (ts_root_daemon and nova_tailscale_ssh | bool)")

	sw := roleFileTasks(t, "roles/tailscale/tasks/ssh-off.yml")
	var steps []int
	for _, name := range []string{"Tailscale SSH off, detached", "the switch lands", "the old connection dropped", "Tailscale SSH is off (over a new",
		"the control node logs in by key on a fresh connection", "the control node got in by key", "kept off",
		"Tailscale SSH's state after the decision", "the play decided, and Tailscale SSH is off", "the switch's receipt"} {
		i := taskIndex(sw, name)
		require.GreaterOrEqual(t, i, 0, "ssh-off.yml: no task %q", name)
		steps = append(steps, i)
	}
	assert.IsIncreasing(t, steps, "switch, reconnect, check, prove the key login, then keep")
	job := sw[steps[0]]
	assert.Equal(t, 0, job["poll"], "detached: the run does not wait on a connection the switch ends")
	assert.NotNil(t, job["async"])
	assert.Equal(t, true, job["become"])
	script := asStrings(job["ansible.builtin.command"].(map[string]any)["argv"])
	require.Len(t, script, 8)
	assert.Equal(t, `: > "$3"; if [ -d "${4%/*}" ]; then : > "$4"; fi; rm -f "$1"; sleep 2; "$0" set --ssh=false; sleep "$2"; if (set -C; : > "$1") 2>/dev/null; then "$0" set --ssh=true; fi; rm -f "$3" "$4"`,
		script[2], "back on at the deadline unless the play claimed the decision first; pending and netwatch held while it runs")
	assert.Equal(t, []string{"{{ ts_bin }}", "{{ nova_tailscale_ssh_keep }}", "{{ nova_tailscale_ssh_deadline }}", "{{ nova_tailscale_ssh_pending }}", "{{ nova_tailscale_ssh_hold }}"}, script[3:])
	d, err = os.ReadFile("roles/tailscale/defaults/main.yml")
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(d, &defaults))
	nw, err := os.ReadFile("roles/netwatch/files/nova-netwatch")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(asText(defaults["nova_tailscale_ssh_hold"]), "/run/nova-netwatch/hold.d/"))
	assert.Contains(t, string(nw), "RUN=${NETWATCH_RUN:-/run/nova-netwatch}", "the hold file is in the directory netwatch reads")
	assert.Contains(t, string(nw), `ls -A "$RUN/hold.d"`, "netwatch holds its reboot for any file there")
	assert.Equal(t, "reset_connection", asText(sw[steps[2]]["ansible.builtin.meta"]))
	login := strings.Join(asStrings(sw[steps[4]]["ansible.builtin.command"].(map[string]any)["argv"]), " ")
	assert.Contains(t, login, "-o ControlPath=none")
	assert.Equal(t, "localhost", asText(sw[steps[4]]["delegate_to"]))
	assert.Equal(t, "ts_key_login.rc == 0", asText(sw[steps[5]]["ansible.builtin.assert"].(map[string]any)["that"]))
	// the play's claim is the same exclusive create as the job's, and kept is reported only on a fresh read of off
	assert.Equal(t, []string{"sh", "-c", `set -C; : > "$1"`, "sh", "{{ nova_tailscale_ssh_keep }}"},
		asStrings(sw[steps[6]]["ansible.builtin.command"].(map[string]any)["argv"]))
	assert.Equal(t, "{{ ts_bin }} debug prefs", asText(sw[steps[7]]["ansible.builtin.command"]))
	assert.Equal(t, "ts_ssh_kept.rc == 0 and not (ts_kept_runssh | bool)", asText(sw[steps[8]]["ansible.builtin.assert"].(map[string]any)["that"]))
}

func asStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, asText(x))
	}
	return out
}

// TestFirewallAllowInIsCheckedThenApplied: nova_firewall_allow_in is refused unless it is a list of plain ufw rule
// words (it reaches a shell), each rule counts as wanted until ufw says it exists, and the set step adds it.
func TestFirewallAllowInIsCheckedThenApplied(t *testing.T) {
	t.Parallel()
	file := "roles/tailscale/tasks/firewall.yml"
	tasks := roleFileTasks(t, file)
	check, pkg := taskIndex(tasks, "nova_firewall_allow_in is a list"), taskIndex(tasks, "the ufw package")
	require.True(t, check >= 0 && check < pkg, "the list is checked before anything is installed or changed")
	assert.Contains(t, asText(tasks[check]["ansible.builtin.assert"].(map[string]any)["that"].([]any)[1]), "reject('match', '^[A-Za-z0-9][A-Za-z0-9 ./:_-]*$')")
	plan := asText(roleTask(t, file, "what ufw has now")["ansible.builtin.shell"])
	assert.Contains(t, plan, "ufw --dry-run allow {{ r }} 2>&1 | grep -q 'Skipping adding existing rule'")
	set := asText(roleTask(t, file, "ufw, set")["ansible.builtin.shell"])
	assert.Contains(t, set, "{% for r in nova_firewall_allow_in %}\nufw allow {{ r }}\n{% endfor %}\nufw --force enable")
	d, err := os.ReadFile("roles/tailscale/defaults/main.yml")
	require.NoError(t, err)
	var defaults map[string]any
	require.NoError(t, yaml.Unmarshal(d, &defaults))
	assert.Equal(t, []any{}, defaults["nova_firewall_allow_in"], "empty by default: the tailnet only")
}

// TestTailscaleRoleReadsUpsKeyFirst: up hands its minted key over as NOVA_FLEET_AUTHKEY, since nova-secrets exec drops
// an inherited TAILSCALE_AUTHKEY when the control seat holds one; the role reads that name first, then
// TAILSCALE_AUTHKEY for a run alone, and only where the key is used (never a fact).
func TestTailscaleRoleReadsUpsKeyFirst(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("roles/tailscale/tasks/main.yml")
	require.NoError(t, err)
	var tasks []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &tasks))
	i := taskIndex(tasks, "join (Linux, macOS)")
	require.GreaterOrEqual(t, i, 0)
	assert.Equal(t, "{{ lookup('ansible.builtin.env', 'NOVA_FLEET_AUTHKEY') or lookup('ansible.builtin.env', 'TAILSCALE_AUTHKEY') }}",
		asText(tasks[i]["vars"].(map[string]any)["ts_authkey"]))
	up := roleTask(t, "roles/tailscale/tasks/main.yml", "tailscale up as")
	assert.Equal(t, "{{ ts_authkey }}", asText(up["ansible.builtin.command"].(map[string]any)["stdin"]))
	assert.NotContains(t, string(b), "set_fact:\n        ts_authkey")
	u := &upRun{tskey: "tskey-auth-x"}
	env := u.env()
	assert.Contains(t, env, "NOVA_FLEET_AUTHKEY=tskey-auth-x")
	assert.Empty(t, u.tskey, "the key reaches one play only")
}

// TestServicesInstallPamSystemdWhereMissing: a minimal Debian or Ubuntu image can lack pam_systemd, and then the
// login's user manager never starts; the play asks dpkg without root and installs the package only where it is missing.
func TestServicesInstallPamSystemdWhereMissing(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("services.yml")
	require.NoError(t, err)
	var plays []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &plays))
	tasks := flattenTasks(t, toTasks(plays[0]["tasks"]))
	ask, put, units := taskIndex(tasks, "is pam_systemd installed"), taskIndex(tasks, "pam_systemd, which the login"), taskIndex(tasks, "one unit per record")
	require.True(t, ask >= 0 && put > ask && units > put, "asked, then installed, before any unit")
	assert.Equal(t, "dpkg-query -W -f=${Status} libpam-systemd", asText(tasks[ask]["ansible.builtin.command"]))
	assert.Nil(t, tasks[ask]["become"], "asking needs no root")
	assert.Equal(t, true, tasks[put]["become"])
	assert.Contains(t, asText(tasks[put]["when"]), "'install ok installed' not in service_pam.stdout")
}

func toTasks(v any) []map[string]any {
	var out []map[string]any
	for _, x := range v.([]any) {
		out = append(out, x.(map[string]any))
	}
	return out
}
