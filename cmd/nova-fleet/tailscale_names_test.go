package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The machine's names and prompt are set where every machine passes: the tailscale role includes names.yml after the
// join, names.yml runs hostname.yml on Linux (WSL included) and prompt.yml everywhere, and join.yml reaches them by
// importing tailscale.yml, with no hostname task of its own. So a joined machine and a re-run machine come out the same.
// The run against a fake machine is tailscale_names_functional_test.go.
func TestNamesRunAtJoinAndAtEveryTailscaleRun(t *testing.T) {
	t.Parallel()
	names := roleTask(t, "roles/tailscale/tasks/main.yml", "the hostname, /etc/hosts, cloud-init and the prompt")
	assert.Equal(t, "names.yml", asText(names["ansible.builtin.include_tasks"]))
	assert.Nil(t, names["when"], "every machine, Macs too, takes names.yml (the prompt)")

	host := roleTask(t, "roles/tailscale/tasks/names.yml", "the hostname, /etc/hosts and cloud-init")
	assert.Equal(t, "hostname.yml", asText(host["ansible.builtin.include_tasks"]))
	assert.Equal(t, "ts_os == 'debian'", asText(host["when"]), "Linux and WSL are renamed, never a Mac")
	prompt := roleTask(t, "roles/tailscale/tasks/names.yml", "the fleet's prompt")
	assert.Equal(t, "prompt.yml", asText(prompt["ansible.builtin.include_tasks"]))
	assert.Nil(t, prompt["when"])

	b, err := os.ReadFile("join.yml")
	require.NoError(t, err)
	var entries []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &entries))
	imported := false
	for _, e := range entries {
		if e["ansible.builtin.import_playbook"] == "tailscale.yml" {
			imported = true
		}
	}
	assert.True(t, imported, "join's tailnet step is tailscale.yml")
	assert.NotContains(t, string(b), "ansible.builtin.hostname", "one path sets the hostname: the tailscale role")
}

// The machine name is checked to be a hostname before the role writes anything on any OS: valid.yml is main.yml's
// first task, unconditional, so it runs ahead of the install, `tailscale up`/`set`, names.yml and the prompt.
func TestNameIsCheckedBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	tasks := roleFileTasks(t, "roles/tailscale/tasks/main.yml")
	require.NotEmpty(t, tasks)
	assert.Equal(t, "valid.yml", asText(tasks[0]["ansible.builtin.include_tasks"]), "the first task of the role")
	assert.Nil(t, tasks[0]["when"], "every OS, Macs included")
	set := taskIndex(tasks, "tailscale set (hostname, ssh)")
	require.Greater(t, set, 0)
	assert.Equal(t, -1, taskIndex(roleFileTasks(t, "roles/tailscale/tasks/hostname.yml"), "a record name that is not a hostname"),
		"one check, in valid.yml")
}

// The prompt the fleet asked for, byte for byte: bash's from the drop-in, the record name for \h on a Mac, zsh's with
// the record name, and the rc line that sources it.
func TestPromptIsTheFleetsPrompt(t *testing.T) {
	t.Parallel()
	task := roleTask(t, "roles/tailscale/tasks/prompt.yml", "~/.config/nova/prompt.")
	vars := task["vars"].(map[string]any)
	assert.Contains(t, asText(vars["pr_bash"]), `PS1='\[\033[36m\]\u@{{ inventory_hostname if pr_darwin else pr_h }}\[\033[00m\]:\w\$ '`)
	assert.Equal(t, `\h`, asText(vars["pr_h"]))
	assert.Contains(t, asText(vars["pr_zsh"]), `PROMPT='%F{cyan}%n@{{ inventory_hostname }}%f:%~%# '`)
	assert.True(t, strings.Contains(asText(vars["pr_bash"]), "case $- in *i*) ;; *) return ;; esac"), "interactive shells only")
	assert.Contains(t, asText(vars["pr_zsh"]), "[[ -o interactive ]] || return")
	line := roleTask(t, "roles/tailscale/tasks/prompt.yml", "~/{{ pr_rc }} sources it")
	assert.Equal(t, "[ -f ~/.config/nova/prompt.{{ pr_shell }} ] && . ~/.config/nova/prompt.{{ pr_shell }}",
		asText(line["ansible.builtin.lineinfile"].(map[string]any)["line"]))
}
