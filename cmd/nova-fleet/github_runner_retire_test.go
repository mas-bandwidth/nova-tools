package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A runner's CI rules are applied as root (tasks/ci-rules.yml), which leaves <runner dir>/.nova-ci-rules owned by root.
// Retiring the runner deletes its directory, so the delete runs as root: as the login it stops at that directory after
// the service is stopped and the runner removed from GitHub, and the env.applied left there keeps a runner registered
// again in that directory from being restarted onto its hooks.
func TestRunnerRetireDeletesTheDirectoryAsRoot(t *testing.T) {
	t.Parallel()
	retire := roleTaskFile(t, "unix-retire.yml")
	del := runnerTask(t, retire, "delete the directory")
	assert.Equal(t, "remove-dir.yml", asText(del["ansible.builtin.include_tasks"]))
	assert.Equal(t, "github_runner_rm", del["loop_control"].(map[string]any)["loop_var"])
	rm := roleTaskFile(t, "remove-dir.yml")
	require.Len(t, rm, 1)
	assert.Equal(t, true, rm[0]["become"], "root deletes what the CI rules left owned by root")
	assert.Equal(t, "not ansible_check_mode", asText(rm[0]["when"]))
}

// Root deletes in a runner login's home only by name, in a parent it has checked is the directory its name holds: a
// login that turned the parent (or the directory) into a link sends root nowhere it did not mean.
func TestRunnerDirectoryIsDeletedThroughNoLink(t *testing.T) {
	t.Parallel()
	sh := asText(roleTaskFile(t, "remove-dir.yml")[0]["ansible.builtin.shell"])
	for _, want := range []string{`cd "$parent"`, `[ -L "$parent" ]`, `ls -di .`, `ls -di "$parent"`, `rm -rf -- "./$name"`} {
		assert.Contains(t, sh, want)
	}
	assert.Less(t, strings.Index(sh, `ls -di "$parent"`), strings.Index(sh, `rm -rf`), "the parent is checked before anything is deleted")
}

// The runner's dependency script runs as root, so root runs its own copy: the cached release, checked against the
// pinned digest and unpacked into a directory of root's, never the copy in a runner login's home.
func TestRunnerDependenciesRunFromRootsOwnCopy(t *testing.T) {
	t.Parallel()
	deps := runnerTask(t, roleTaskFile(t, "unix-runner.yml"), "the runner's system dependencies (Linux)")
	sh := asText(deps["ansible.builtin.shell"])
	for _, want := range []string{"mktemp -d", "sha256sum -c", `"$stage/x/bin/installdependencies.sh"`} {
		assert.Contains(t, sh, want)
	}
	assert.NotContains(t, sh, "{{ github_runner_dir }}/bin/installdependencies.sh")
	assert.Less(t, strings.Index(sh, "sha256sum -c"), strings.Index(sh, "installdependencies.sh"))
	assert.Equal(t, true, deps["become"])
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
	one := runnerTask(t, main, "one url, named with -e github_runner_url")
	assert.Equal(t, "scope.yml", asText(one["ansible.builtin.include_tasks"]))
	assert.Equal(t, "github_runner_url is defined", asText(one["when"]))
	record := runnerTask(t, main, "every runner of the machine record")
	assert.Equal(t, "record.yml", asText(record["ansible.builtin.include_tasks"]))
	assert.Equal(t, "github_runner_url is not defined", asText(record["when"]))
	assert.Equal(t, len(main)-1, taskIndex(main, "every runner of the machine record"), "the runs are the last tasks")
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
