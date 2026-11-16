package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// github_runner_deploy_keys_test.go is the structural half of the runner login's read-only deploy keys
// (roles/github-runner/tasks/deploy-keys.yml; cmd/nova-fleet/README.md, "Deploy keys"); the run on localhost is
// github_runner_deploy_keys_functional_test.go.

// The include runs only when keys are named, and --tags deploy-keys runs it alone.
func TestDeployKeysIncludeIsTaggedAndEmptyByDefault(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []any{}, runnerDefaults(t)["github_runner_deploy_keys"], "nothing is installed unless named")
	inc := runnerTask(t, roleTaskFile(t, "main.yml"), "the runner login's deploy keys")
	file := inc["ansible.builtin.include_tasks"].(map[string]any)
	assert.Equal(t, "deploy-keys.yml", asText(file["file"]))
	assert.Equal(t, []any{"deploy-keys"}, file["apply"].(map[string]any)["tags"], "every task inside carries the tag")
	assert.Equal(t, []any{"deploy-keys"}, inc["tags"])
	assert.Equal(t, "github_runner_deploy_keys | length > 0", asText(inc["when"]))
}

// The value is read only through the machine's seat, inside a no_log task, and reaches the key file through a pipe as
// the runner login; the URL rewrite is the whole clone URL for fetching, a push stays on https, and no credential helper
// is written anywhere.
func TestDeployKeysScopeAndSecrecy(t *testing.T) {
	t.Parallel()
	tasks := roleTaskFile(t, "deploy-keys.yml")

	names := runnerTask(t, tasks, "the names in this machine's seat, for the deploy keys")
	assert.Equal(t, false, names["check_mode"], "read under --check too")
	assert.Contains(t, asText(runnerTask(t, tasks, "every deploy key is in this machine's seat")["ansible.builtin.fail"].(map[string]any)["msg"]), "nova-secrets grant")
	assert.Less(t, taskIndex(tasks, "every deploy key is in this machine's seat"), taskIndex(tasks, "the runner login's ~/.ssh"), "refused before anything changes")

	key := runnerTask(t, tasks, "each deploy key, from this machine's seat")
	assert.Equal(t, true, key["no_log"])
	sh := asText(key["ansible.builtin.shell"])
	assert.Contains(t, sh, "set -o pipefail;")
	assert.Contains(t, sh, "{{ github_runner_win_secrets_exec }} {{ item.secret | quote }} -- sh -c 'printf \"%s\" \"${{ item.secret }}\"'")
	assert.Contains(t, sh, "'sudo -n -u ' ~ (github_runner_user | quote)", "the key file is written as the runner login")
	assert.Contains(t, sh, `ssh-keygen -y -f "$k.new"`, "a value that is no key never replaces the file")
	assert.Contains(t, sh, `if cmp -s "$k.new" "$k"`, "a second run changes nothing")
	assert.Contains(t, asText(key["when"]), "not ansible_check_mode")
	receipt := runnerTask(t, tasks, "the deploy keys' receipt")
	assert.Contains(t, asText(receipt["ansible.builtin.debug"].(map[string]any)["msg"]), "select('match', '^DEPLOY-KEY-')")

	for _, name := range []string{"the runner login's ~/.ssh", "the host keys a deploy key's connection accepts",
		"one ssh host alias per deploy key, offering that key alone", "each deploy key's URL fetched through its alias",
		"each deploy key's URL lists as the runner login"} {
		task := runnerTask(t, tasks, name)
		assert.Equal(t, "{{ github_runner_user }}", task["become_user"], "task %q runs as the runner login", name)
	}

	ssh := asText(runnerTask(t, tasks, "one ssh host alias per deploy key, offering that key alone")["ansible.builtin.blockinfile"].(map[string]any)["block"])
	for _, want := range []string{"Host {{ k.alias }}", "IdentityFile {{ k.key }}", "IdentitiesOnly yes", "StrictHostKeyChecking yes",
		"UserKnownHostsFile {{ github_runner_home }}/.ssh/deploy-keys.known_hosts", "BatchMode yes"} {
		assert.Contains(t, ssh, want)
	}
	assert.True(t, strings.HasSuffix(ssh, "{% endfor %}\nMatch all\n"), "the last Host block is closed, so what follows the block stays global")
	git := asText(runnerTask(t, tasks, "each deploy key's URL fetched through its alias")["ansible.builtin.blockinfile"].(map[string]any)["block"])
	assert.Contains(t, git, "[url \"{{ k.ssh }}\"]\n  insteadOf = {{ k.url }}\n")
	assert.Contains(t, git, "[url \"{{ k.url }}\"]\n  pushInsteadOf = {{ k.url }}\n")

	proof := runnerTask(t, tasks, "each deploy key's URL lists as the runner login")
	assert.Equal(t, []any{"git", "ls-remote", "{{ item.url }}", "HEAD"}, proof["ansible.builtin.command"].(map[string]any)["argv"])

	for _, task := range tasks {
		assert.NotContains(t, strings.ToLower(fmt.Sprint(task)), "credential", "task %q: no credential helper", asText(task["name"]))
	}
	known := asText(runnerDefaults(t)["github_runner_deploy_known_hosts"])
	assert.Contains(t, known, "github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl\n")
}
