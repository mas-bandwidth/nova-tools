package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// A runner's CI rules are applied as root (tasks/ci-rules.yml), which leaves <runner dir>/.nova-ci-rules owned by root.
// Retiring the runner deletes its directory, so the delete runs as root: as the login it stops at that directory after
// the service is stopped and the runner removed from GitHub, and the env.applied left there keeps a runner registered
// again in that directory from being restarted onto its hooks.
func TestRunnerRetireDeletesTheDirectoryAsRoot(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("roles/github-runner/tasks/unix-retire.yml")
	require.NoError(t, err)
	var raw []any
	require.NoError(t, yaml.Unmarshal(b, &raw))
	var del map[string]any
	for _, task := range flattenTasks(t, mapsOf(t, raw)) {
		if f, ok := task["ansible.builtin.file"].(map[string]any); ok && f["state"] == "absent" {
			del = task
		}
	}
	require.NotNil(t, del, "unix-retire.yml deletes the runner's directory")
	assert.Equal(t, "{{ not ansible_check_mode }}", asText(del["become"]), "root deletes what the CI rules left owned by root")
}
