package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// play_failures_test.go reads the plays and roles as data (no subprocess) for defects a run cannot show on this
// machine: a WSL step whose exit code was tr's, a tailnet address read before a rename, a check-mode receipt that
// claimed a check it never made, and commands a user of the nova-fleet binary cannot run.

// roleFileTasks is a role task file's tasks, each block's tasks inlined.
func roleFileTasks(t *testing.T, file string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(file)
	require.NoError(t, err)
	var tasks []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &tasks))
	return flattenTasks(t, tasks)
}

func roleTask(t *testing.T, file, prefix string) map[string]any {
	t.Helper()
	tasks := roleFileTasks(t, file)
	i := taskIndex(tasks, prefix)
	require.GreaterOrEqual(t, i, 0, "%s: no task %q", file, prefix)
	return tasks[i]
}

// TestCIRulesWSLStepFailsWhenPowerShellFails: the Windows side's apply is piped through tr; without pipefail the
// step's exit code was tr's and a failing powershell passed.
func TestCIRulesWSLStepFailsWhenPowerShellFails(t *testing.T) {
	t.Parallel()
	task := roleTask(t, "roles/github-runner/tasks/ci-rules.yml", "apply them to the Windows runners")
	script := asText(task["ansible.builtin.shell"])
	require.Contains(t, script, "| tr -d '\\r'")
	assert.Contains(t, script, "pipefail", "the pipeline's exit code is powershell's")
	assert.Equal(t, "/bin/bash", asText(task["args"].(map[string]any)["executable"]), "pipefail needs bash")
}

// TestCIRulesCheckSaysItDidNotCheck: --check never runs nova-ci-rules, so its receipt cannot say nothing changed.
func TestCIRulesCheckSaysItDidNotCheck(t *testing.T) {
	t.Parallel()
	task := roleTask(t, "roles/github-runner/tasks/ci-rules.yml", "the CI rules' receipt")
	msg := asText(task["ansible.builtin.debug"].(map[string]any)["msg"])
	assert.NotContains(t, msg, "nothing changed")
	assert.Contains(t, msg, "not checked")
}

// TestTailscaleReadsTheNodeAgainAfterARename: the MagicDNS name recorded in nova-config is read after tailscale set
// renamed the node, not the name it had before.
func TestTailscaleReadsTheNodeAgainAfterARename(t *testing.T) {
	t.Parallel()
	file := "roles/tailscale/tasks/main.yml"
	set := roleTask(t, file, "tailscale set (hostname, ssh)")
	reg := asText(set["register"])
	require.NotEmpty(t, reg, "tailscale set registers its result")
	for _, name := range []string{"the state after the join", "the node, as it is now"} {
		assert.Contains(t, asText(roleTask(t, file, name)["when"]), reg+" is changed", "task %q runs after a rename", name)
	}
}

// TestPlayTextNamesNovaFleetCommands: what `nova-fleet show` prints and what a refusal says to run are commands a
// user of the binary has, not ansible-playbook against a repository path.
func TestPlayTextNamesNovaFleetCommands(t *testing.T) {
	t.Parallel()
	plays, err := filepath.Glob("*.yml")
	require.NoError(t, err)
	for _, p := range plays {
		b, err := os.ReadFile(p)
		require.NoError(t, err)
		assert.NotContains(t, string(b), "ansible-playbook -i ./inventory", "%s: the header gives the nova-fleet command", p)
		assert.NotContains(t, string(b), "Run cmd/nova-fleet/", p)
	}
	join, err := os.ReadFile("join.yml")
	require.NoError(t, err)
	assert.Contains(t, string(join), "run: nova-fleet join <machine>")
	install, err := os.ReadFile("roles/nova-tools-install/tasks/main.yml")
	require.NoError(t, err)
	assert.NotContains(t, string(install), "-e nova_tools_version=vX.Y.Z")
	assert.Contains(t, string(install), "--tag vX.Y.Z")
}

// join's login profile makes a bare nova-secrets exec work on a bench: the pushed store, the machine's seat, and sops's
// directory on PATH (/opt/homebrew/bin on a Mac).
func TestJoinProfileGivesABareExecItsStoreSeatAndSops(t *testing.T) {
	t.Parallel()
	join, err := os.ReadFile("join.yml")
	require.NoError(t, err)
	for _, line := range []string{
		`NOVA_SECRETS_STORE="{{ nova_secrets_store }}"; export NOVA_SECRETS_STORE`,
		`NOVA_SECRETS_SEAT="{{ nova_seat }}"; export NOVA_SECRETS_SEAT`,
		`case ":$PATH:" in *":{{ nova_sops | dirname }}:"*) ;; *) PATH="$PATH:{{ nova_sops | dirname }}"; export PATH;; esac`,
	} {
		assert.Contains(t, string(join), line)
	}
	// ssh <machine> '<cmd>' reads no login profile: the same lines go first in ~/.bashrc, or ~/.zshenv for zsh
	assert.Contains(t, string(join), `insertbefore: BOF`)
	assert.Contains(t, string(join), `'.zshenv' if (ansible_facts['user_shell'] | default('') | basename) == 'zsh' else '.bashrc'`)
	assert.Equal(t, 2, strings.Count(string(join), `block: "{{ join_login_env }}"`), "the login profile and the non-login rc carry the same lines")
}
