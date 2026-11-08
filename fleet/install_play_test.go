package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// install_play_test.go pins the end of roles/nova-tools-install/tasks/main.yml: after
// the binaries are in place the role lists what else an older install left on the
// machine and refuses the host when it finds any (fleet/README.md, "install",
// Leftovers). Deploying v2 on 2026-11-06 left ~25 v1 binaries, user and system
// units and launchd agents running beside the new binaries on every host for a day,
// and the play said nothing.

// installRoleTasks reads the role's main task list, each block's tasks inlined.
func installRoleTasks(t *testing.T) []map[string]any {
	t.Helper()
	b, err := os.ReadFile("roles/nova-tools-install/tasks/main.yml")
	require.NoError(t, err)
	var tasks []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &tasks))
	return flattenTasks(t, tasks)
}

// taskIndex is the position of the first task whose name has the prefix, or -1.
func taskIndex(tasks []map[string]any, prefix string) int {
	for i, task := range tasks {
		if strings.HasPrefix(asText(task["name"]), prefix) {
			return i
		}
	}
	return -1
}

func TestInstallRoleEndsWithTheLeftoverInventory(t *testing.T) {
	t.Parallel()
	tasks := installRoleTasks(t)

	// The read: one find over the bin directory, the login's unit directory and the
	// system unit directory, each with the names an older install used.
	i := taskIndex(tasks, "what else is on the machine")
	require.GreaterOrEqual(t, i, 0, "no leftover inventory task in the install role")
	find, ok := tasks[i]["ansible.builtin.find"].(map[string]any)
	require.True(t, ok, "the leftover inventory is not an ansible.builtin.find (a read, so --check runs it too)")
	assert.Equal(t, true, find["use_regex"])
	assert.Equal(t, "{{ item.path }}", asText(find["path"])+asText(find["paths"]))
	dirs := taskVar(t, tasks[i], "nova_tools_leftover_dirs")
	for _, want := range []string{
		"nova_tools_bin_dir",
		"'/.config/systemd/user' if nova_tools_os == 'linux' else '/Library/LaunchAgents'",
		"nova_tools_system_unit_dir",
		"nova-.*", "novadog-.*", "mirror-refresh", "disk-guard", "ci-hold-broker", "com[.]nova[.]",
		// the allowed names: the release's binaries (plus nova_tools_keep) and the units of this machine's service records
		"nova_tools_bin_ok | map('regex_escape')",
		"nova-bus[.]hook[.].+[.]plist|nova-bus-hook-.+[.]service",
		"nova_tools_fleet_owns.user", "nova_tools_fleet_owns.system",
		"if nova_services is defined else []",
		"nova-service-(' ~ nova_tools_records ~ ')[.](service|timer)",
		"com[.]nova[.]service[.](' ~ nova_tools_records ~ ')[.]plist",
	} {
		assert.Contains(t, dirs, want, "nova_tools_leftover_dirs")
	}
	// Allowed binaries: the release's, nova_tools_keep, every manifest of another repository's release, the fleet's own roles'.
	assert.Equal(t, "{{ nova_tools_release_names + nova_tools_keep + nova_tools_other_names + nova_tools_fleet_owns.bin }}", taskVar(t, tasks[i], "nova_tools_bin_ok"))
	records := taskVar(t, tasks[i], "nova_tools_records")
	assert.Contains(t, records, "nova_services | default([])", "an inventory without nova_services (a plain hosts file) names no record")
	assert.Contains(t, records, "map(attribute='name')")

	// It runs last: after every task that puts a binary in place, on Unix, for nova-tools' own release.
	for _, before := range []string{"the binaries, verified against SHA256SUMS", "the binaries, copied from the local build",
		"the bin directory and the binaries (Windows)", "the retired binaries, moved aside"} {
		assert.Less(t, taskIndex(tasks, before), i, "%q does not run before the leftover inventory", before)
	}
	// The whole check, the INSTALL line and the refusal included, is one block: a --repo install prints no line.
	b := taskIndex(tasks, "the leftovers of an older install")
	require.GreaterOrEqual(t, b, 0)
	when := asText(tasks[b]["when"])
	assert.Contains(t, when, "not nova_tools_win")
	assert.Contains(t, when, "nova_tools_repo == 'mas-bandwidth/nova-tools'")
	assert.Equal(t, len(tasks)-b-1, len(mapsOf(t, tasks[b]["block"])), "a leftover task outside the block")
	for _, name := range []string{"the manifests of other", "the names they list", "what the fleet's own roles write"} {
		assert.Greater(t, i, taskIndex(tasks, name), "%q does not run before the read", name)
		assert.Greater(t, taskIndex(tasks, name), b)
	}

	// The receipt, one line per host, then the refusal with its fix.
	j := taskIndex(tasks, "the leftovers, one line per host")
	require.Greater(t, j, i, "no INSTALL line after the leftover inventory")
	debug, ok := tasks[j]["ansible.builtin.debug"].(map[string]any)
	require.True(t, ok)
	msg := asText(debug["msg"])
	assert.Contains(t, msg, "INSTALL {{ inventory_hostname }} leftovers={{ nova_tools_leftovers | length }}")
	assert.Contains(t, msg, "nova_tools_leftovers | join(', ')")
	assert.Contains(t, msg, "NOTE: the inventory carries no nova_services", "units skipped without a word")

	k := taskIndex(tasks, "nothing an older install left behind")
	require.Greater(t, k, j, "no refusal after the INSTALL line")
	a, ok := tasks[k]["ansible.builtin.assert"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "nova_tools_leftovers | length == 0", asText(a["that"]))
	assert.Contains(t, asText(a["fail_msg"]), "remove them (nova-fleet services for units of a record; by hand for the rest), then install again")
	assert.Equal(t, len(tasks)-1, k, "the refusal is not the role's last task")

	// A --repo install records its release's names for this check, after its binaries are in place.
	m := taskIndex(tasks, "the manifest, one name a line")
	require.Greater(t, m, taskIndex(tasks, "the retired binaries, moved aside"))
	cp, ok := tasks[m]["ansible.builtin.copy"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "{{ nova_tools_manifest_dir }}/{{ nova_tools_repo | replace('/', '_') }}.names", asText(cp["dest"]))
	assert.Contains(t, asText(cp["content"]), "nova_tools_release_names")
	assert.Contains(t, asText(tasks[taskIndex(tasks, "this release's names, recorded")]["when"]), "nova_tools_repo != 'mas-bandwidth/nova-tools'")
	fo := taskIndex(tasks, "what the fleet's own roles write")
	ownsVars, err := yaml.Marshal(tasks[fo]["vars"])
	require.NoError(t, err)
	for _, want := range []string{"nova_secrets_refresh_owns", "nova_health_owns", "nova_postgres_owns", "/redis/defaults/main.yml"} {
		assert.Contains(t, string(ownsVars), want)
	}
	assert.Contains(t, asText(tasks[fo]["ansible.builtin.set_fact"].(map[string]any)["nova_tools_fleet_owns"]), "nova_redis_unit | default(nova_tools_redis_defaults.nova_redis_unit)")

	// The release's names are read once, beside the install list, so --names does not shrink the allowed set.
	n := taskIndex(tasks, "the tools to install")
	require.GreaterOrEqual(t, n, 0)
	set, ok := tasks[n]["ansible.builtin.set_fact"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, asText(set["nova_tools_release_names"]), "nova_tools_listed")
	assert.Contains(t, asText(set["nova_tools_install_names"]), "else nova_tools_listed")
}

// The leftover list is built from the find results: every file (never a directory)
// whose name the entry's ok pattern does not match.
func TestInstallRoleLeftoversAreTheUnlistedFiles(t *testing.T) {
	t.Parallel()
	tasks := installRoleTasks(t)
	i := taskIndex(tasks, "the leftovers: binaries no release lists, units no role or record names")
	require.GreaterOrEqual(t, i, 0)
	set, ok := tasks[i]["ansible.builtin.set_fact"].(map[string]any)
	require.True(t, ok)
	expr := asText(set["nova_tools_leftovers"])
	for _, want := range []string{"nova_tools_found.results | default([])", "not f.isdir", "is not match(r.item.ok)", "f.path | basename"} {
		assert.Contains(t, expr, want)
	}
	assert.Regexp(t, regexp.MustCompile(`\| sort`), expr, "the names are sorted, so two runs print the same line")
}

// The defaults the inventory reads: nothing kept beside the release unless asked, and the system unit directory.
func TestInstallRoleLeftoverDefaults(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("roles/nova-tools-install/defaults/main.yml")
	require.NoError(t, err)
	var d map[string]any
	require.NoError(t, yaml.Unmarshal(b, &d))
	assert.Equal(t, []any{}, d["nova_tools_keep"])
	assert.Equal(t, "/etc/systemd/system", d["nova_tools_system_unit_dir"])
	assert.Equal(t, "{{ nova_home }}/.local/share/nova/installed", d["nova_tools_manifest_dir"])
}

// Each role's nova_<role>_owns is held to what its tasks write where the leftover check
// reads, so the allow list cannot drift from the roles: every nova-*.service/.timer named
// in the tasks, every unit stem under /etc/systemd/system, the LaunchAgent label, the script.
func TestInstallRoleLeftoverOwnsAreTheRolesNames(t *testing.T) {
	t.Parallel()
	unit := regexp.MustCompile(`\bnova-[a-z0-9-]+[.](service|timer)\b`)
	stem := regexp.MustCompile(`/etc/systemd/system/(nova[a-z0-9-]*)[.]`)
	for role, key := range map[string]string{"secrets-refresh": "nova_secrets_refresh_owns", "health": "nova_health_owns", "postgres": "nova_postgres_owns"} {
		b, err := os.ReadFile("roles/" + role + "/defaults/main.yml")
		require.NoError(t, err)
		var d map[string]map[string][]string
		var raw map[string]any
		require.NoError(t, yaml.Unmarshal(b, &raw))
		ob, err := yaml.Marshal(map[string]any{key: raw[key]})
		require.NoError(t, err)
		require.NoError(t, yaml.Unmarshal(ob, &d))
		owns := d[key]
		require.NotEmpty(t, owns, "%s has no %s", role, key)
		all := strings.Join(append(append(append([]string{}, owns["bin"]...), owns["user"]...), owns["system"]...), " ")
		tb, err := os.ReadFile("roles/" + role + "/tasks/main.yml")
		require.NoError(t, err)
		for _, u := range unit.FindAllString(string(tb), -1) {
			assert.Contains(t, all, u, "%s writes %s and its %s does not name it", role, u, key)
		}
		for _, m := range stem.FindAllStringSubmatch(string(tb), -1) {
			assert.Contains(t, strings.Join(owns["system"], " "), m[1]+".", "%s writes %s under /etc/systemd/system and its %s does not name it", role, m[1], key)
		}
	}
	tb, err := os.ReadFile("roles/secrets-refresh/tasks/main.yml")
	require.NoError(t, err)
	assert.Contains(t, string(tb), `sr_script: "{{ nova_bin_dir }}/nova-secrets-refresh"`, "nova_secrets_refresh_owns.bin names this script")
	assert.Contains(t, string(tb), "sr_label: com.nova.secrets-refresh\n", "nova_secrets_refresh_owns.user names this label's plist")
}
