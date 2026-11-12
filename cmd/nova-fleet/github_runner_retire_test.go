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

// A runner is never stopped or restarted under a running job: retiring waits for its job first, and a changed unit (or a
// removed drop-in) restarts the runner only after the wait (tasks/wait-idle.yml).
func TestRunnerStopsOnlyWhenIdle(t *testing.T) {
	t.Parallel()
	retire := roleTaskFile(t, "unix-retire.yml")
	assert.Less(t, taskIndex(retire, "the job it is running ends first"), taskIndex(retire, "stop and remove the service"))
	assert.Equal(t, "wait-idle.yml", asText(runnerTask(t, retire, "the job it is running ends first")["ansible.builtin.include_tasks"]))
	runner := roleTaskFile(t, "unix-runner.yml")
	assert.Less(t, taskIndex(runner, "no drop-in beside the unit (Linux)"), taskIndex(runner, "the job it is running ends before the changed unit"))
	assert.Less(t, taskIndex(runner, "the job it is running ends before the changed unit"), taskIndex(runner, "the service is enabled at boot and running (Linux)"))
	assert.Less(t, taskIndex(runner, "the job it is running ends before the changed daemon"), taskIndex(runner, "load the daemon, or reload it"))
}

// Without -e github_runner_url the run is the machine record's (record.yml); with it, that one url's (scope.yml).
func TestRunnerRunIsTheRecordsWithoutAURL(t *testing.T) {
	t.Parallel()
	main := roleTaskFile(t, "main.yml")
	require.Len(t, main, 2)
	assert.Equal(t, "scope.yml", asText(main[0]["ansible.builtin.include_tasks"]))
	assert.Equal(t, "github_runner_url is defined", asText(main[0]["when"]))
	assert.Equal(t, "record.yml", asText(main[1]["ansible.builtin.include_tasks"]))
	assert.Equal(t, "github_runner_url is not defined", asText(main[1]["when"]))
}

// A held runner (github_runner_hold, e.g. one running a multi-hour model check nothing resumes) is neither waited on nor
// restarted or reloaded, on Linux and on macOS; the run says RUNNER-HELD so the restart is owed, and by default
// nothing is held.
func TestRunnerHeldIsNeitherWaitedOnNorRestarted(t *testing.T) {
	t.Parallel()
	runner := roleTaskFile(t, "unix-runner.yml")
	for _, name := range []string{
		"the job it is running ends before the changed unit restarts it (Linux)",
		"the job it is running ends before the changed daemon reloads it (macOS)",
		"load the daemon, or reload it where its file changed (macOS)",
	} {
		assert.Contains(t, asText(runnerTask(t, runner, name)["when"]), "not in github_runner_hold", name)
	}
	restart := runnerTask(t, runner, "the service is enabled at boot and running (Linux)")
	assert.Contains(t, asText(restart["ansible.builtin.systemd"].(map[string]any)["state"]), "not in github_runner_hold")
	for _, name := range []string{
		"a held runner is not waited on or restarted (Linux)",
		"a held runner is not waited on or reloaded (macOS)",
	} {
		held := runnerTask(t, runner, name)
		assert.Contains(t, asText(held["ansible.builtin.debug"].(map[string]any)["msg"]), "RUNNER-HELD", name)
		assert.Contains(t, asText(held["when"]), "in github_runner_hold", name)
	}
	d, err := os.ReadFile("roles/github-runner/defaults/main.yml")
	require.NoError(t, err)
	assert.Contains(t, string(d), "\ngithub_runner_hold: []\n")
}
