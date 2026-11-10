package main

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// secrets_refresh_play_test.go pins the secrets-refresh role to push mode (D8c, 2026-11-07: "Nothing fetches on a
// timer; nothing mirrors everything to every machine."). The role installs no timer, cron line, LaunchAgent or
// clone.

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

// TestSecretsRefreshInstallsNoTimer fails when any fetch on a clock comes into the role: a task
// that writes a unit, plist or script, enables or starts a unit, adds a cron line, clones or loads an agent; or a
// file in the role that schedules anything.
func TestSecretsRefreshInstallsNoTimer(t *testing.T) {
	t.Parallel()
	for _, task := range secretsRefreshTasks(t) {
		name := asText(task["name"])
		for _, module := range []string{"ansible.builtin.template", "ansible.builtin.copy", "ansible.builtin.git", "ansible.builtin.systemd_service", "ansible.builtin.systemd", "ansible.builtin.cron", "community.general.launchd"} {
			assert.NotContains(t, task, module, "task %q: the role writes no unit, plist, script or clone", name)
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

// TestSecretsRefreshRefusesARunThatWouldPushNothing: without nova_seal_store the role has nothing to push, so a
// machine that is refreshed (nova_secrets_refresh true) fails, naming the variable, before any task changes
// anything; a run that only checked the store and exited 0 would look like a delivery.
func TestSecretsRefreshRefusesARunThatWouldPushNothing(t *testing.T) {
	t.Parallel()
	tasks := secretsRefreshTasks(t)
	refusal := -1
	for i, task := range tasks {
		when := asText(task["when"])
		if task["ansible.builtin.fail"] != nil && strings.Contains(when, "nova_seal_store is not defined") {
			refusal = i
			assert.Contains(t, when, "nova_secrets_refresh", "a machine with nova_secrets_refresh false pushes nothing by design")
			assert.Contains(t, asText(task["ansible.builtin.fail"].(map[string]any)["msg"]), "run: nova-fleet secrets-refresh -- -e nova_seal_store=")
			break
		}
	}
	require.GreaterOrEqual(t, refusal, 0, "no task refuses a run without nova_seal_store")
	for _, task := range tasks[:refusal] {
		assert.Contains(t, task, "ansible.builtin.fail", "task %q runs before the refusal", asText(task["name"]))
	}
}

// TestSecretsRefreshPushesOnlyASecretsStore: the push deletes what the clone lacks, so an empty directory or one that
// is not a store would empty every machine's store. Before the push the role checks the clone holds .sops.yaml and
// recovery.pub (what nova-secrets init writes) and refuses otherwise; a store of zero files is never a success.
func TestSecretsRefreshPushesOnlyASecretsStore(t *testing.T) {
	t.Parallel()
	check, push := -1, -1
	var last map[string]any
	for i, task := range secretsRefreshTasks(t) {
		if st, ok := task["ansible.builtin.stat"].(map[string]any); ok && strings.Contains(asText(st["path"]), "nova_seal_store") {
			check = i
			loop := fmt.Sprint(task["loop"])
			assert.Contains(t, loop, ".sops.yaml")
			assert.Contains(t, loop, "recovery.pub")
			assert.Equal(t, "localhost", task["delegate_to"], "the clone is on the control node")
		}
		if task["ansible.posix.synchronize"] != nil {
			push = i
		}
		if task["ansible.builtin.assert"] != nil {
			last = task
		}
	}
	require.GreaterOrEqual(t, check, 0, "nothing checks that nova_seal_store is a secrets store")
	assert.Less(t, check, push, "the store check runs before the push")
	require.NotNil(t, last)
	assert.Contains(t, fmt.Sprint(last["ansible.builtin.assert"]), "'0'", "the last check refuses a store of zero files")
}
