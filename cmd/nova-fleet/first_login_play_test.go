package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestJoinLoginPlayBecomesRootThroughAFirstLogin: join's login play connects as nova_join_root_user, becomes root
// only when that login is not root, and gives the fleet's login the first login's own keys (a cloud image's root
// keys may only print "log in as ubuntu").
func TestJoinLoginPlayBecomesRootThroughAFirstLogin(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("join.yml")
	require.NoError(t, err)
	var plays []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &plays))
	var login map[string]any
	for _, p := range plays {
		if asText(p["name"]) == "the login (Linux, as root, or as the first login through sudo)" {
			login = p
		}
	}
	require.NotNil(t, login, "join.yml has no login play")
	assert.Equal(t, "{{ nova_join_root_user != 'root' }}", asText(login["become"]))
	assert.Equal(t, "{{ nova_join_root_user }}", asText(login["vars"].(map[string]any)["ansible_user"]))
	tasks := flattenTasks(t, toTasks(login["tasks"]))
	home := taskIndex(tasks, "the home of the first login")
	keys := taskIndex(tasks, "the authorized keys of")
	require.True(t, home >= 0 && keys > home, "the first login's home is read before its keys are copied")
	src := asText(tasks[keys]["ansible.builtin.copy"].(map[string]any)["src"])
	assert.Contains(t, src, "getent_passwd'][nova_join_root_user][4] ~ '/.ssh/authorized_keys'")
	assert.NotContains(t, src, "/root/.ssh")
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
