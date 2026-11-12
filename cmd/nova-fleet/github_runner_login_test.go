package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// github_runner_login_test.go pins the runner login (cmd/nova-fleet/README.md, "The runner login"): a Linux machine's
// runners may run as a login of their own (github_runner_user), so a CI job has no root. That login is locked, in no
// group but its own and kvm, refused by sshd, granted nothing by sudo, and its home is its own; a runner whose unit runs
// it from another login's home moves after its job ends. The run on a real machine is the proof in the pull request.

func groupVars(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("group_vars/all.yml")
	require.NoError(t, err)
	var all map[string]any
	require.NoError(t, yaml.Unmarshal(b, &all))
	return all
}

// One definition every play reads: the runner login and its home, the fleet login's by default.
func TestRunnerLoginIsTheFleetLoginByDefault(t *testing.T) {
	t.Parallel()
	all := groupVars(t)
	assert.Equal(t, "{{ ansible_facts['user_id'] }}", asText(all["github_runner_user"]))
	assert.Equal(t, "{{ ansible_facts['user_dir'] if github_runner_user == ansible_facts['user_id'] else '/home/' ~ github_runner_user }}",
		asText(all["github_runner_home"]))
	d := runnerDefaults(t)
	assert.Equal(t, "{{ github_runner_home ~ '/actions-runner' }}", asText(d["github_runner_base_dir"]))
	b, err := os.ReadFile("roles/github-runner/templates/runner.service.j2")
	require.NoError(t, err)
	assert.Contains(t, string(b), "\nUser={{ github_runner_user }}\n")
}

// The login of its own is made before anything reads its home, and only when it is not the fleet login.
func TestRunnerLoginIsMadeFirst(t *testing.T) {
	t.Parallel()
	main := roleTaskFile(t, "main.yml")
	facts, _ := runnerTask(t, main, "whose runners these are")["ansible.builtin.set_fact"].(map[string]any)
	assert.Equal(t, "{{ github_runner_user != ansible_facts['user_id'] }}", asText(facts["github_runner_other"]))
	login := runnerTask(t, main, "the runner login of its own (Linux)")
	assert.Equal(t, "unix-login.yml", asText(login["ansible.builtin.include_tasks"]))
	assert.Equal(t, "github_runner_other | bool", asText(login["when"]))
	assert.Less(t, taskIndex(main, "the runner login of its own"), taskIndex(main, "one url"))
	assert.Less(t, taskIndex(main, "the runner login of its own"), taskIndex(main, "every runner of the machine record"))
}

// Locked, its own group and kvm only (append false drops any other, sudo with it), its home 0750, sshd refusing it,
// and sudo's own answer for it read as root and failed on any grant.
func TestRunnerLoginHasNoRootAndNoSSH(t *testing.T) {
	t.Parallel()
	tasks := roleTaskFile(t, "unix-login.yml")
	user, _ := runnerTask(t, tasks, "the runner login (locked, its own group and kvm only)")["ansible.builtin.user"].(map[string]any)
	assert.Equal(t, "{{ github_runner_user }}", user["name"])
	assert.Equal(t, true, user["password_lock"])
	assert.Equal(t, false, user["append"])
	assert.Equal(t, "{{ 'kvm' if github_runner_kvm.rc == 0 else '' }}", user["groups"])
	assert.Nil(t, user["password"])

	home, _ := runnerTask(t, tasks, "the runner login's home is its own")["ansible.builtin.file"].(map[string]any)
	assert.Equal(t, "0750", home["mode"])
	assert.Equal(t, "{{ github_runner_user }}", home["owner"])

	sshd := runnerTask(t, tasks, "sshd refuses the runner login")
	cp, _ := sshd["ansible.builtin.copy"].(map[string]any)
	assert.Contains(t, asText(cp["content"]), "\nDenyUsers {{ github_runner_user }}\n")
	assert.Equal(t, "/usr/sbin/sshd -t -f %s", cp["validate"])

	sudo := runnerTask(t, tasks, "sudo grants the runner login nothing")
	cmd, _ := sudo["ansible.builtin.command"].(map[string]any)
	assert.Equal(t, []any{"sudo", "-n", "-l", "-U", "{{ github_runner_user }}"}, cmd["argv"])
	assert.Equal(t, "'is not allowed to run sudo' not in github_runner_sudo.stdout", asText(sudo["failed_when"]))
	assert.Equal(t, false, sudo["check_mode"], "read under --check too")
	assert.Equal(t, "C", sudo["environment"].(map[string]any)["LC_ALL"], "sudo's words are matched in the C locale")

	// sshd's own settings deny it, so a drop-in sshd never reads fails the run
	sshdT := runnerTask(t, tasks, "sshd's settings deny the runner login")
	assert.Equal(t, "/usr/sbin/sshd", sshdT["ansible.builtin.command"].(map[string]any)["argv"].([]any)[0])
	assert.Contains(t, asText(sshdT["failed_when"]), "denyusers")

	// an account that holds the name already is taken only as a runner login: never an admin's, never a system account
	assert.Less(t, taskIndex(tasks, "an existing account is a runner login"), taskIndex(tasks, "the runner login (locked"))
	exists := fmt.Sprint(runnerTask(t, tasks, "an existing account is a runner login, never an admin's or a system account")["ansible.builtin.assert"].(map[string]any)["that"])
	assert.Contains(t, exists, "| int >= 1000")
	assert.Contains(t, exists, "difference([github_runner_user, 'kvm']) | length == 0")
}

// config.sh runs as the runner login (become_user), pipelined: the token never lands in a file, and the runner's
// credentials are the login's.
func TestRunnerConfigRunsAsTheRunnerLogin(t *testing.T) {
	t.Parallel()
	for _, f := range []string{"unix-register.yml", "unix-retire.yml"} {
		found := false
		for _, task := range roleTaskFile(t, f) {
			sh, ok := task["ansible.builtin.shell"].(string)
			if !ok || !strings.Contains(sh, "./config.sh") {
				continue
			}
			found = true
			assert.Equal(t, "{{ github_runner_other }}", task["become"], f)
			assert.Equal(t, "{{ github_runner_user }}", task["become_user"], f)
			vars, _ := task["vars"].(map[string]any)
			assert.Equal(t, true, vars["ansible_pipelining"], f)
		}
		assert.True(t, found, "%s runs config.sh", f)
	}
}

// A runner whose unit runs it from another directory moves: its job ends (wait-idle on the old directory), its unit
// stops, and only then does it register here (config.sh --replace), so no job is cut off; the old directory goes once
// the new unit runs it. A held runner is refused, not moved.
func TestRunnerMovesOnlyWhenIdle(t *testing.T) {
	t.Parallel()
	runner := flattenTasks(t, roleTaskFile(t, "unix-runner.yml"))
	order := []string{
		"the directory its unit runs it from (Linux)",
		"where it moves from",
		"a moving runner is not held",
		"the moving runner's job ends first",
		"the moving runner is stopped where it runs",
		"register with GitHub",
		"the unit (Linux)",
		"the service is enabled at boot and running (Linux)",
		"the moved runner's old directory is deleted (Linux)",
	}
	for i := 1; i < len(order); i++ {
		assert.Less(t, taskIndex(runner, order[i-1]), taskIndex(runner, order[i]), order[i])
	}
	wait := runnerTask(t, runner, "the moving runner's job ends first")
	assert.Equal(t, "wait-idle.yml", asText(wait["ansible.builtin.include_tasks"]))
	vars, _ := wait["vars"].(map[string]any)
	assert.Equal(t, "{{ github_runner_old_dir }}", vars["github_runner_idle_dir"])
	move := runnerTask(t, runner, "the move, and the registration here")
	rescue := mapsOf(t, move["rescue"])
	require.NotEmpty(t, rescue, "a failed move has a rescue")
	assert.Equal(t, "a failed move starts the runner where it ran", rescue[0]["name"])
	held := runnerTask(t, runner, "a moving runner is not held")
	assert.Contains(t, asText(held["ansible.builtin.assert"].(map[string]any)["that"]), "not in github_runner_hold")
	b, err := os.ReadFile("roles/github-runner/tasks/wait-idle.yml")
	require.NoError(t, err)
	assert.Contains(t, string(b), "{{ github_runner_idle_dir | default(github_runner_dir) }}/bin/[R]unner.Worker")
}
