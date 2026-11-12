package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// github_runner_git_probe_test.go pins that the workspace git probe (roles/github-runner/tasks/git-probe.yml,
// cmd/nova-fleet/README.md "github-runners") runs only when asked: a copy and a fetch per clone is too heavy for every
// run. Its runs are github_runner_git_probe_functional_test.go.
func TestRunnerGitProbeIsOffByDefault(t *testing.T) {
	t.Parallel()
	assert.Equal(t, false, runnerDefaults(t)["nova_runner_git_probe"])
	tasks := roleTaskFile(t, "main.yml")
	task := runnerTask(t, tasks, "the workspace clones that make git abort in the checkout's fetch (-e nova_runner_git_probe=true)")
	assert.Equal(t, "git-probe.yml", task["ansible.builtin.include_tasks"])
	assert.Equal(t, "nova_runner_git_probe | bool", asText(task["when"]))
	assert.Equal(t, len(tasks)-1, taskIndex(tasks, "the workspace clones that make git abort"), "after the runners are converged")
}
