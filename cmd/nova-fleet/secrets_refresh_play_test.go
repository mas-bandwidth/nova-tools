package main

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// secrets_refresh_play_test.go pins the secrets-refresh role to push mode (D8c, 2026-11-07: "Nothing fetches on a
// timer; nothing mirrors everything to every machine."). Pull mode put a timer on each machine that pulled a git
// clone of the store on a clock; it is retired. The role refuses nova_secrets_repo first, removes on every machine
// what pull mode installed, and installs no timer, cron line, LaunchAgent or clone.

const secretsRefreshRole = "roles/secrets-refresh"

// secretsRefreshTasks is the role's task list, each block's tasks inlined.
func secretsRefreshTasks(t *testing.T) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(secretsRefreshRole + "/tasks/main.yml")
	require.NoError(t, err)
	var raw []any
	require.NoError(t, yaml.Unmarshal(b, &raw))
	return flattenTasks(t, mapsOf(t, raw))
}

func TestSecretsRefreshRefusesPullModeFirst(t *testing.T) {
	t.Parallel()
	tasks := secretsRefreshTasks(t)
	require.NotEmpty(t, tasks)
	first := tasks[0]
	assert.Contains(t, first, "ansible.builtin.fail", "the role's first task stops a run that sets nova_secrets_repo")
	assert.Contains(t, asText(first["when"]), "nova_secrets_repo")
}

func TestSecretsRefreshRemovesThePullTimerEverywhere(t *testing.T) {
	t.Parallel()
	var removal map[string]any
	for _, task := range secretsRefreshTasks(t) {
		if task["block"] != nil && strings.Contains(asText(task["name"]), "timer removed") {
			removal = task
		}
	}
	require.NotNil(t, removal, "no block removes the pull timer")
	assert.NotContains(t, removal, "when", "the pull timer's removal runs on every machine")
}

// TestSecretsRefreshInstallsNoTimer fails when pull mode, or any fetch on a clock, comes back into the role: a task
// that writes a unit, plist or script, enables or starts a unit, adds a cron line, clones or loads an agent; or a
// file in the role that schedules anything.
func TestSecretsRefreshInstallsNoTimer(t *testing.T) {
	t.Parallel()
	for _, task := range secretsRefreshTasks(t) {
		name := asText(task["name"])
		for _, module := range []string{"ansible.builtin.template", "ansible.builtin.copy", "ansible.builtin.git", "ansible.builtin.systemd_service", "ansible.builtin.systemd", "community.general.launchd"} {
			assert.NotContains(t, task, module, "task %q: the role writes no unit, plist, script or clone", name)
		}
		if cron, ok := task["ansible.builtin.cron"].(map[string]any); ok {
			assert.Equal(t, "absent", cron["state"], "task %q: a cron line is only ever removed", name)
		}
		if f, ok := task["ansible.builtin.file"].(map[string]any); ok && strings.Contains(asText(f["path"]), "sr_script") {
			assert.Equal(t, "absent", f["state"], "task %q: the pull script is only ever removed", name)
		}
	}
	err := fs.WalkDir(os.DirFS(secretsRefreshRole), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(secretsRefreshRole + "/" + p)
		if err != nil {
			return err
		}
		text := string(b)
		for _, clock := range []string{"StartInterval", "StartCalendarInterval", "OnUnitActiveSec", "OnCalendar", "OnBootSec", "launchctl bootstrap", "enable --now", "enable-linger"} {
			assert.NotContains(t, text, clock, "%s/%s: nothing in secrets-refresh fetches on a clock (D8c)", secretsRefreshRole, p)
		}
		return nil
	})
	require.NoError(t, err)
}
