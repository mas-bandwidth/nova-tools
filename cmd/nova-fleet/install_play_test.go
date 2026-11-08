package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// install_play_test.go pins the end of roles/nova-tools-install/tasks/main.yml: after
// the binaries are in place the role lists what else an older install left on the
// machine and refuses the host when it finds any (cmd/nova-fleet/README.md, "install",
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
		"nova_tools_user_unit_dir",
		"nova_tools_system_unit_dir",
		"nova-.*", "com[.]nova[.]",
		// every file in the bin directory is read (dotted names and .bak copies too), and every retired unit
		"'patterns': ['^.*$']", "nova_tools_retired_unit_re",
		// the allowed names: the release's binaries and the units of this machine's service records
		"nova_tools_bin_ok | map('regex_escape')",
		"nova-bus[.]hook[.].+[.]plist|nova-bus-hook-.+[.]service",
		"nova_tools_fleet_owns.user", "nova_tools_fleet_owns.system",
		// a headless Mac's units: /Library/LaunchDaemons, com.nova.* plists, a record's allowed
		"'^nova.*$' if nova_tools_os == 'linux' else '^com[.]nova[.].*$'", "else '^com[.]nova[.]service[.](' ~ nova_tools_records ~ ')[.]plist$'",
		"if nova_services is defined else []",
		"nova-service-(' ~ nova_tools_records ~ ')[.](service|timer)",
		"com[.]nova[.]service[.](' ~ nova_tools_records ~ ')[.]plist",
	} {
		assert.Contains(t, dirs, want, "nova_tools_leftover_dirs")
	}
	// Allowed binaries (no hand-kept list): the release's, every manifest of another repository's release, the fleet's own roles'.
	assert.Equal(t, "{{ nova_tools_release_names + nova_tools_other_names + nova_tools_fleet_owns.bin + ((nova_join_owns | default({})).bin | default([])) }}", taskVar(t, tasks[i], "nova_tools_bin_ok"))
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
	assert.Contains(t, asText(a["fail_msg"]), "retire them, then install again: {{ nova_tools_retire_how | join('; ') }}")
	assert.NotContains(t, asText(a["fail_msg"]), "by hand")
	// The fix is the plays that retire them, the names filled in; only what no play retires is named on its own.
	how := asText(tasks[taskIndex(tasks, "the leftovers, and the plays that retire them")]["ansible.builtin.set_fact"].(map[string]any)["nova_tools_retire_how"])
	for _, want := range []string{"nova-fleet install --tag", "nova_tools_retire_vars | to_json(separators=(',', ':'))",
		"nova-fleet services -- -l ", "{'nova_retire_units': nova_tools_sorted.unit_names | unique | sort} | to_json(separators=(',', ':'))", "no play retires "} {
		assert.Contains(t, how, want)
	}
	// The install command names the binaries and nova-push-credential while a git helper line runs it.
	assert.Equal(t, "{{ (nova_tools_sorted.bin + (['nova-push-credential'] if nova_tools_gitcred_left else [])) | unique | sort }}",
		taskVar(t, tasks[taskIndex(tasks, "the leftovers, and the plays that retire them")], "nova_tools_retire_bin"))
	rv := taskVar(t, tasks[taskIndex(tasks, "the leftovers, and the plays that retire them")], "nova_tools_retire_vars")
	assert.Contains(t, rv, "{'nova_tools_remove': nova_tools_retire_bin}")
	assert.Contains(t, rv, "{'nova_tools_remove_units': nova_tools_sorted.retired_units | unique | sort}")
	// JSON extra vars: a key=value -e is a string, never the list the plays want.
	assert.NotContains(t, how, "nova_tools_remove='")
	assert.NotContains(t, how, "nova_retire_units='")
	// A binary nothing accounts for and no retired row names is a warning naming it, never a refusal.
	w := taskIndex(tasks, "binaries no release or manifest accounts for")
	require.Greater(t, w, j)
	assert.Contains(t, asText(tasks[w]["ansible.builtin.debug"].(map[string]any)["msg"]), "WARNING unrecorded={{ nova_tools_sorted.unrecorded | length }}: {{ nova_tools_sorted.unrecorded | sort | join(', ') }}")
	assert.Equal(t, "nova_tools_sorted.unrecorded | length > 0", asText(tasks[w]["when"]))
	assert.NotContains(t, asText(tasks[w]["ansible.builtin.debug"].(map[string]any)["msg"]), "remove")
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
// whose name the entry's ok pattern does not match, sorted by what retires it. A binary
// is a leftover only when nova-tools (or nova-sprint, with its manifest here) retired
// it; any other is unrecorded, a warning.
func TestInstallRoleLeftoversAreTheUnlistedFiles(t *testing.T) {
	t.Parallel()
	tasks := installRoleTasks(t)
	i := taskIndex(tasks, "the leftovers: binaries no release lists, units no role or record names")
	require.GreaterOrEqual(t, i, 0)
	set, ok := tasks[i]["ansible.builtin.set_fact"].(map[string]any)
	require.True(t, ok)
	expr := asText(set["nova_tools_sorted"])
	for _, want := range []string{"nova_tools_found.results | default([])", "not f.isdir", "is not match(d.item.ok)", "f.path | basename",
		"{%- elif n in nova_tools_own_retired or (n | regex_replace('[.](bak|old|before).*$', '')) in nova_tools_own_retired -%}{%- set _ = r.bin.append(n) -%} {%- else -%}{%- set _ = r.unrecorded.append(n) -%}",
		"n is match(nova_tools_retired_unit_re)", "r.retired_units.append(n)",
		"n is match(nova_tools_unit_shape)", "r.noplay.append(n)"} {
		assert.Contains(t, expr, want)
	}
	assert.Equal(t, `^com[.]nova[.](?:service|loop)[.](.+)[.]plist$|^nova-(?:service|loop)-(.+)[.](?:service|timer)$`, taskVar(t, tasks[i], "nova_tools_unit_shape"),
		"the shapes services.yml retires by nova_retire_units")
	rn := taskIndex(tasks, "the retired names and units")
	require.GreaterOrEqual(t, rn, 0)
	assert.Contains(t, taskVar(t, tasks[rn], "nova_tools_retired_rows"), "retired-tools.txt")
	rf := tasks[rn]["ansible.builtin.set_fact"].(map[string]any)
	assert.NotContains(t, rf, "nova_tools_fleet_recorded", "a manifest never turns a binary that is not retired into a refusal")
	left := asText(tasks[taskIndex(tasks, "the leftovers, and the plays that retire them")]["ansible.builtin.set_fact"].(map[string]any)["nova_tools_leftovers"])
	assert.Regexp(t, regexp.MustCompile(`\| sort`), left, "the names are sorted, so two runs print the same line")
	assert.NotContains(t, left, "unrecorded", "an unrecorded binary is never a leftover")
}

// The defaults the inventory reads: nothing kept beside the release unless asked, and the system unit directory.
func TestInstallRoleLeftoverDefaults(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("roles/nova-tools-install/defaults/main.yml")
	require.NoError(t, err)
	var d map[string]any
	require.NoError(t, yaml.Unmarshal(b, &d))
	assert.NotContains(t, d, "nova_tools_keep")
	// No binary is kept by hand: nova-push-credential is retired (Glenn, 2026-11-08).
	assert.NotContains(t, d, "nova_tools_hand_kept")
	assert.NotContains(t, d, "nova_tools_fleet_repos")
	assert.Equal(t, []any{}, d["nova_tools_remove_units"])
	assert.Equal(t, true, d["nova_tools_unit_manager"])
	assert.Contains(t, d["nova_tools_remove"], "nova-push-credential")
	assert.Equal(t, "^!?({{ [nova_tools_bin_dir, ansible_facts['env']['HOME'] ~ '/.local/bin'] | unique | map('regex_escape', 'posix_basic') | map('regex_replace', '$', '/nova-push-credential') | join('|') }})$", d["nova_tools_gitcred_value"],
		"the unset matches the script's own path alone, so the other helpers stay")
	tb, err := os.ReadFile("roles/nova-tools-install/tasks/main.yml")
	require.NoError(t, err)
	assert.NotContains(t, string(tb), "hand_kept")
	tasks := installRoleTasks(t)
	u := taskIndex(tasks, "the git credential helpers that run nova-push-credential, unset")
	require.GreaterOrEqual(t, u, 0, "retiring nova-push-credential leaves git's credential helper lines")
	assert.Equal(t, []any{"git", "config", "--global", "--unset-all", "{{ item }}", "{{ nova_tools_gitcred_value }}"},
		tasks[u]["ansible.builtin.command"].(map[string]any)["argv"])
	assert.Contains(t, asText(tasks[u]["when"]), "nova_tools_remove_enabled | bool")
	assert.Contains(t, asText(tasks[u]["when"]), "'nova-push-credential' in nova_tools_remove")
	assert.Greater(t, taskIndex(tasks, "the git credential helpers that run nova-push-credential, as they are now"), u, "the leftover read comes after the unset")
	assert.Equal(t, "{{ '/Library/LaunchDaemons' if nova_tools_os == 'darwin' else '/etc/systemd/system' }}", d["nova_tools_system_unit_dir"],
		"a headless Mac's units are LaunchDaemons (services.yml, nova_launchd_domain)")
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
	// A role that writes a nova unit or plist and declares nothing is caught: every role's
	// tasks are read, and only the roles above (and redis, by nova_redis_unit) may name one.
	dirs, err := os.ReadDir("roles")
	require.NoError(t, err)
	plist := regexp.MustCompile(`com[.]nova[.][a-z0-9.-]+[.]plist|\bsr_label\b`)
	declared := map[string]bool{"secrets-refresh": true, "health": true, "postgres": true, "redis": true, "nova-tools-install": true}
	for _, d := range dirs {
		files, _ := filepath.Glob("roles/" + d.Name() + "/tasks/*.yml")
		for _, f := range files {
			tb, err := os.ReadFile(f)
			require.NoError(t, err)
			if unit.Match(tb) || stem.Match(tb) || plist.Match(tb) {
				assert.True(t, declared[d.Name()], "%s writes a nova unit or plist (%s) and declares no nova_<role>_owns for the leftover check", d.Name(), f)
			}
		}
	}
	tb, err := os.ReadFile("roles/secrets-refresh/tasks/main.yml")
	require.NoError(t, err)
	assert.Contains(t, string(tb), `sr_script: "{{ nova_bin_dir }}/nova-secrets-refresh"`, "nova_secrets_refresh_owns.bin names this script")
	assert.Contains(t, string(tb), "sr_label: com.nova.secrets-refresh\n", "nova_secrets_refresh_owns.user names this label's plist")
}

// Retired units are stopped and moved aside, never deleted, only when asked; the
// system directory takes sudo. Every v1 name rowan-tools shipped is a retired row.
func TestInstallRoleRetiresV1UnitsAndNames(t *testing.T) {
	t.Parallel()
	tasks := installRoleTasks(t)
	k := taskIndex(tasks, "the retired units, stopped and moved aside")
	require.GreaterOrEqual(t, k, 0)
	assert.Contains(t, asText(tasks[k]["when"]), "nova_tools_remove_enabled | bool")
	sh := asText(tasks[k]["ansible.builtin.shell"])
	assert.Contains(t, sh, "mv {{ path | quote }} {{ aside | quote }}/")
	assert.NotContains(t, sh, "rm ")
	assert.Equal(t, "{{ item.item.0 }}/retired-{{ now(fmt='%Y-%m-%d') }}", taskVar(t, tasks[k], "aside"))
	assert.Equal(t, "{{ system and nova_tools_unit_manager | bool }}", asText(tasks[k]["become"]))
	b, err := os.ReadFile("retired-tools.txt")
	require.NoError(t, err)
	for _, n := range []string{"winlock", "captain-reap", "rowan-row", "rowan-inbox", "bench-row", "sprint-table-redis", "tools-aside", "friend-autotake",
		"friend-queue", "emma-wait", "johnny-wait", "zhi-turn", "nca-loop.sh", "scratch-clone", "install-github-read-ssh", "landred", "bats",
		"nova-card-push", "mirror-refresh", "nova-push-credential", "ci-hold-wait", "stage-schema"} {
		assert.Regexp(t, regexp.MustCompile(`(?m)^`+regexp.QuoteMeta(n)+"\t[^\t]+\tretired"), string(b), n)
	}
	// A backup of a retired name is retired: its name before .bak, .old or .before (the
	// role's regex_replace, the same in Go's regexp) is a retired row; a backup of a name
	// not retired is not.
	backup := regexp.MustCompile(`[.](bak|old|before).*$`)
	for name, retired := range map[string]bool{"winlock.bak-4slots": true, "bench-hygiene.sh.before-fleet-20260919": true, "scratch-clone.bak": true,
		"tlacheck.old-2026-10-19": false, "sbcl.before-fleet-lisp-20260919": false} {
		stem := backup.ReplaceAllString(name, "")
		assert.Equal(t, retired, regexp.MustCompile(`(?m)^`+regexp.QuoteMeta(stem)+"\t[^\t]+\tretired").Match(b), "%s (stem %s)", name, stem)
	}
	u, err := os.ReadFile("retired-units.txt")
	require.NoError(t, err)
	for _, g := range []string{"nova-swarm-*.service", "com.nova.runner.*.plist", "novadog-*", "nova-dashboard-pull.*", "ci-hold-broker.service"} {
		assert.Contains(t, string(u), g+"\t")
	}
	// XDP on space may still use these: not retired until it is checked.
	assert.NotContains(t, string(u), "nova-xdp-")
	assert.NotContains(t, string(u), "nova-watchdog-module")
}

// join.yml's binaries in the bin directory are the fleet's own, allowed by name from
// group_vars (nova_join_owns), held to what join.yml unpacks and downloads.
func TestJoinOwnsAreJoinsBinaries(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("group_vars/all.yml")
	require.NoError(t, err)
	var g map[string]any
	require.NoError(t, yaml.Unmarshal(b, &g))
	owns, ok := g["nova_join_owns"].(map[string]any)
	require.True(t, ok, "group_vars has no nova_join_owns")
	assert.ElementsMatch(t, []any{"age", "age-keygen", "sops"}, owns["bin"])
	assert.Contains(t, asText(g["nova_sops"]), "nova_bin_dir ~ '/sops'")
	assert.Contains(t, asText(g["nova_age_keygen"]), "nova_bin_dir ~ '/age-keygen'")
	j, err := os.ReadFile("join.yml")
	require.NoError(t, err)
	assert.Contains(t, string(j), "include: [age/age, age/age-keygen]")
}

// nova_tools_remove_units stops units: it may name only what cmd/nova-fleet/retired-units.txt
// retires, refused first otherwise (tailscaled.service would stop tailscale). The
// globs are read from the file, and Glenn's node exporter on the studio is not in it.
func TestInstallRoleStopsOnlyRetiredUnits(t *testing.T) {
	t.Parallel()
	tasks := installRoleTasks(t)
	i := taskIndex(tasks, "every unit to retire is a retired one")
	require.GreaterOrEqual(t, i, 0, "nova_tools_remove_units is not checked against cmd/nova-fleet/retired-units.txt")
	a := tasks[i]["ansible.builtin.assert"].(map[string]any)
	assert.Equal(t, "nova_tools_remove_units | reject('match', nova_tools_retired_unit_re) | list | length == 0", asText(a["that"]))
	assert.Contains(t, asText(a["fail_msg"]), "nova_tools_remove_units names {{ nova_tools_remove_units | reject('match', nova_tools_retired_unit_re) | join(', ') }}")
	assert.Less(t, i, taskIndex(tasks, "which retired units are on the machine"), "the refusal comes before any unit is touched")
	assert.Less(t, i, taskIndex(tasks, "the platform"), "the refusal comes before anything changes")
	b, err := os.ReadFile("roles/nova-tools-install/defaults/main.yml")
	require.NoError(t, err)
	var d map[string]any
	require.NoError(t, yaml.Unmarshal(b, &d))
	assert.Contains(t, asText(d["nova_tools_unit_globs"]), "/../../retired-units.txt")
	assert.Equal(t, "{{ '^(' ~ (nova_tools_unit_globs | map('regex_replace', '[.]', '[.]') | map('regex_replace', '[*]', '.*') | join('|')) ~ ')$' }}", d["nova_tools_retired_unit_re"])
	u, err := os.ReadFile("retired-units.txt")
	require.NoError(t, err)
	assert.NotContains(t, string(u), "node-exporter", "com.nova.node-exporter.plist is Glenn's node exporter, live on the studio")
	assert.NotContains(t, string(u), "tailscale")
}
