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
		"nova_tools_release_names + nova_tools_keep",
		"nova-service-(' ~ nova_tools_records ~ ')[.](service|timer)",
		"com[.]nova[.]service[.](' ~ nova_tools_records ~ ')[.]plist",
	} {
		assert.Contains(t, dirs, want, "nova_tools_leftover_dirs")
	}
	records := taskVar(t, tasks[i], "nova_tools_records")
	assert.Contains(t, records, "nova_services | default([])", "an inventory without nova_services (a plain hosts file) names no record")
	assert.Contains(t, records, "map(attribute='name')")

	// It runs last: after every task that puts a binary in place, on Unix, for nova-tools' own release.
	for _, before := range []string{"the binaries, verified against SHA256SUMS", "the binaries, copied from the local build",
		"the bin directory and the binaries (Windows)", "the retired binaries, moved aside"} {
		assert.Less(t, taskIndex(tasks, before), i, "%q does not run before the leftover inventory", before)
	}
	when := asText(tasks[i]["when"])
	assert.Contains(t, when, "not nova_tools_win")
	assert.Contains(t, when, "nova_tools_repo == 'mas-bandwidth/nova-tools'")

	// The receipt, one line per host, then the refusal with its fix.
	j := taskIndex(tasks, "the leftovers, one line per host")
	require.Greater(t, j, i, "no INSTALL line after the leftover inventory")
	debug, ok := tasks[j]["ansible.builtin.debug"].(map[string]any)
	require.True(t, ok)
	msg := asText(debug["msg"])
	assert.Contains(t, msg, "INSTALL {{ inventory_hostname }} leftovers={{ nova_tools_leftovers | length }}")
	assert.Contains(t, msg, "nova_tools_leftovers | join(', ')")

	k := taskIndex(tasks, "nothing an older install left behind")
	require.Greater(t, k, j, "no refusal after the INSTALL line")
	a, ok := tasks[k]["ansible.builtin.assert"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "nova_tools_leftovers | length == 0", asText(a["that"]))
	assert.Contains(t, asText(a["fail_msg"]), "remove them (nova-fleet services for units of a record; by hand for the rest), then install again")
	assert.Equal(t, len(tasks)-1, k, "the refusal is not the role's last task")

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
	i := taskIndex(tasks, "the leftovers: binaries the release does not list, units no record names")
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
}
