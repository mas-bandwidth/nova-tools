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

func toTasks(v any) []map[string]any {
	var out []map[string]any
	for _, x := range v.([]any) {
		out = append(out, x.(map[string]any))
	}
	return out
}
