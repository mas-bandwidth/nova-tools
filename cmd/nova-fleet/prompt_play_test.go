package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// prompt_play_test.go is the structural half of the prompt play: the block it writes, rendered for bash and zsh, its
// marker lines, and a role that writes the rc files only through blockinfile, so a second run is a no-op.
// prompt_play_functional_test.go runs the role through ansible-core.

const promptRole = "roles/prompt"

// promptDefaults is roles/prompt/defaults/main.yml.
func promptDefaults(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(promptRole + "/defaults/main.yml")
	require.NoError(t, err)
	var d map[string]string
	require.NoError(t, yaml.Unmarshal(b, &d))
	return d
}

// renderPrompt fills the two variables the role sets, as Jinja would.
func renderPrompt(text, name, kind string) string {
	return strings.NewReplacer("{{ prompt_name }}", name, "{{ prompt_kind }}", kind).Replace(text)
}

// The block is networknext's relay prompt: the name and kind in cyan, the reset, the directory with ~, a literal #.
func TestPromptBlockRendersForBashAndZsh(t *testing.T) {
	t.Parallel()
	d := promptDefaults(t)
	assert.Equal(t, `PS1='\[\033[36m\]hulk [linux] \[\033[00m\]\w # '`, renderPrompt(d["nova_prompt_bash"], "hulk", "linux"))
	assert.Equal(t, `PS1='\[\033[36m\]captain [wsl2] \[\033[00m\]\w # '`, renderPrompt(d["nova_prompt_bash"], "captain", "wsl2"))
	assert.Equal(t, `PS1='%F{cyan}studio [mac] %f%~ # '`, renderPrompt(d["nova_prompt_zsh"], "studio", "mac"))
	for _, k := range []string{"nova_prompt_bash", "nova_prompt_zsh"} {
		assert.NotContains(t, d[k], "\n", "%s is one line", k)
		assert.NotContains(t, d[k], `\e]0;`, "%s sets no window title", k)
		assert.NotContains(t, d[k], `\033]`, "%s sets no window title", k)
	}
}

// The markers name the play, so a reader of ~/.bashrc knows what owns the block, and BEGIN and END differ.
func TestPromptMarkers(t *testing.T) {
	t.Parallel()
	m := promptDefaults(t)["nova_prompt_marker"]
	require.Contains(t, m, "{mark}")
	begin, end := strings.Replace(m, "{mark}", "BEGIN", 1), strings.Replace(m, "{mark}", "END", 1)
	assert.Equal(t, "# BEGIN nova-fleet prompt (nova-tools cmd/nova-fleet prompt.yml)", begin)
	assert.Equal(t, "# END nova-fleet prompt (nova-tools cmd/nova-fleet prompt.yml)", end)
}

// The rc files are written only by blockinfile with the play's marker: it replaces what lies between the markers and
// nothing else, which is what makes a second run a no-op. The kind is mac, wsl2 or linux, and a name that could break
// out of the single quotes is refused before anything is written.
func TestPromptRoleWritesOnlyTheManagedBlock(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(promptRole + "/tasks/main.yml")
	require.NoError(t, err)
	var raw []any
	require.NoError(t, yaml.Unmarshal(b, &raw))
	tasks := flattenTasks(t, mapsOf(t, raw))
	var paths []string
	assertAt := -1
	for i, task := range tasks {
		name := asText(task["name"])
		for _, module := range []string{"ansible.builtin.lineinfile", "ansible.builtin.copy", "ansible.builtin.template", "ansible.builtin.shell", "ansible.builtin.command", "ansible.builtin.replace"} {
			assert.NotContains(t, task, module, "task %q: the rc files change only through blockinfile", name)
		}
		if task["ansible.builtin.assert"] != nil {
			assertAt = i
		}
		if bi, ok := task["ansible.builtin.blockinfile"].(map[string]any); ok {
			assert.Greater(t, i, assertAt, "task %q writes before the name is checked", name)
			assert.GreaterOrEqual(t, assertAt, 0, "task %q writes before the name is checked", name)
			assert.Equal(t, "{{ nova_prompt_marker }}", bi["marker"], "task %q", name)
			assert.Nil(t, bi["state"], "task %q", name)
			assert.Nil(t, bi["insertbefore"], "task %q: the block goes at the end, after the stock prompt", name)
			paths = append(paths, asText(bi["path"]))
		}
	}
	assert.Equal(t, []string{"{{ nova_home }}/.bashrc", "{{ nova_home }}/.zshrc"}, paths)
	text := string(b)
	for _, want := range []string{"'mac' if ansible_facts['system'] == 'Darwin'", "'wsl2' if 'microsoft' in (ansible_facts['kernel'] | lower) else 'linux'",
		"prompt_name: \"{{ inventory_hostname }}\"", "match('^[A-Za-z0-9][A-Za-z0-9._-]*$')"} {
		assert.Contains(t, text, want)
	}
}
