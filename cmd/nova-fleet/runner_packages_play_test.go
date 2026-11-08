package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// runner_packages_play_test.go pins the runner-packages play (cmd/nova-fleet/README.md, "runner-packages"): the packages a CI
// runner's tests need, from nova_runner_packages, installed as packages and nothing more. The tests start their own
// PostgreSQL (initdb, pg_ctl), so the role starts no server, runs no `brew services` and enables no unit; and a package
// already on the machine is adopted as it is, so every install task works only on what the presence check found
// missing (mini-m5's postgresql@18 is adopted with changed=0).

const runnerPackagesRole = "roles/runner-packages"

// runnerPackagesTasks is every task of the role, from each of its task files, blocks inlined, keyed by file.
func runnerPackagesTasks(t *testing.T) map[string][]map[string]any {
	t.Helper()
	files, err := filepath.Glob(runnerPackagesRole + "/tasks/*.yml")
	require.NoError(t, err)
	require.NotEmpty(t, files, "the role has no task files")
	out := map[string][]map[string]any{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		var raw []any
		require.NoError(t, yaml.Unmarshal(b, &raw), f)
		out[filepath.Base(f)] = flattenTasks(t, mapsOf(t, raw))
	}
	return out
}

// taskText is everything a task says, as YAML: its module arguments, loop and when.
func taskText(t *testing.T, task map[string]any) string {
	t.Helper()
	b, err := yaml.Marshal(task)
	require.NoError(t, err)
	return string(b)
}

func TestRunnerPackagesPlayTargetsTheRunnerGroup(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("runner-packages.yml")
	require.NoError(t, err)
	assert.NotEmpty(t, header(string(b), "purpose"), "the play's purpose line is what nova-fleet plays prints")
	var docs []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &docs))
	require.Len(t, docs, 1)
	assert.Equal(t, "runner", docs[0]["hosts"], "the play is for the runner group only")
	assert.NotContains(t, docs[0], "become", "brew refuses root: the play runs as the login, a task becomes root only where apt needs it")
	roles, _ := docs[0]["roles"].([]any)
	assert.Equal(t, []any{"runner-packages"}, roles)
	assert.True(t, slices.Contains(plays(tree), "runner-packages"), "nova-fleet lists the play")
}

func TestRunnerPackagesVarsLeaveLinuxUnchanged(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("group_vars/all.yml")
	require.NoError(t, err)
	var vars struct {
		Packages map[string][]string `yaml:"nova_runner_packages"`
	}
	require.NoError(t, yaml.Unmarshal(b, &vars))
	assert.Equal(t, map[string][]string{"darwin": {"postgresql@18"}, "debian": {}}, vars.Packages)
}

// TestRunnerPackagesStartsNoServer fails when a task starts, enables or registers a service: a service module, a
// LaunchAgent or unit file, `brew services`, pg_ctl, initdb or launchctl.
func TestRunnerPackagesStartsNoServer(t *testing.T) {
	t.Parallel()
	modules := []string{"ansible.builtin.service", "ansible.builtin.systemd", "ansible.builtin.systemd_service", "community.general.launchd",
		"ansible.builtin.template", "ansible.builtin.copy", "ansible.builtin.cron", "community.general.homebrew_services", "ansible.windows.win_service"}
	for file, tasks := range runnerPackagesTasks(t) {
		for _, task := range tasks {
			name := asText(task["name"])
			for _, m := range modules {
				assert.NotContains(t, task, m, "%s: task %q: the role installs packages and starts nothing", file, name)
			}
			text := taskText(t, task)
			for _, word := range []string{"services", "pg_ctl", "initdb", "launchctl", "systemctl", "postgres -D", "enabled: true"} {
				assert.NotContains(t, text, word, "%s: task %q: the role installs packages and starts nothing", file, name)
			}
		}
	}
}

// TestRunnerPackagesInstallsOnlyWhatIsMissing fails on an install task that does not work from the presence check:
// each one loops over rp_missing (the wanted packages the check did not find), is skipped under --check, and comes
// after the task that registers what is present. The --check twin prints WOULD-INSTALL for the same list.
func TestRunnerPackagesInstallsOnlyWhatIsMissing(t *testing.T) {
	t.Parallel()
	installs, would := 0, 0
	for file, tasks := range runnerPackagesTasks(t) {
		checked := false
		for _, task := range tasks {
			name := asText(task["name"])
			text := taskText(t, task)
			if strings.Contains(text, "rp_present:") {
				checked = true
			}
			installer := strings.Contains(text, " install ") || strings.Contains(text, " install\n") ||
				task["ansible.builtin.apt"] != nil || task["ansible.builtin.package"] != nil || task["community.general.homebrew"] != nil
			if strings.Contains(text, "WOULD-INSTALL") {
				would++
				assert.Contains(t, asText(task["when"]), "ansible_check_mode", "%s: task %q", file, name)
				assert.Contains(t, text, "rp_missing", "%s: task %q reports what is missing", file, name)
				continue
			}
			if !installer {
				continue
			}
			installs++
			assert.True(t, checked, "%s: task %q installs before the presence check", file, name)
			assert.Contains(t, text, "rp_missing", "%s: task %q installs only what the presence check found missing", file, name)
			assert.Contains(t, asText(task["when"]), "not ansible_check_mode", "%s: task %q changes nothing under --check", file, name)
			assert.NotContains(t, text, "upgrade", "%s: task %q never upgrades what is there", file, name)
			assert.NotContains(t, text, "reinstall", "%s: task %q never reinstalls what is there", file, name)
		}
	}
	assert.Equal(t, 2, installs, "one install task per OS (brew on darwin, apt on debian)")
	assert.Equal(t, 2, would, "one WOULD-INSTALL task per OS")
}

// TestRunnerPackagesReportsOneLinePerHost pins the receipt line.
func TestRunnerPackagesReportsOneLinePerHost(t *testing.T) {
	t.Parallel()
	found := 0
	for _, tasks := range runnerPackagesTasks(t) {
		for _, task := range tasks {
			if text := taskText(t, task); strings.Contains(text, "RUNNER-PACKAGES host=") {
				found++
				for _, field := range []string{"host={{ inventory_hostname }}", "wanted=", "present=", "installed="} {
					assert.Contains(t, text, field)
				}
				assert.Equal(t, false, task["changed_when"], "the receipt changes nothing")
			}
		}
	}
	assert.Equal(t, 1, found, "one RUNNER-PACKAGES line per host")
}
