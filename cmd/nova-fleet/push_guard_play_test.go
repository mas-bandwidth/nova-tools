package main

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// push_guard_play_test.go is the structural half of the push-guard play: every refusal comes before the first write,
// the pre-push hook runs the guard before the repository's own hook and stops on its refusal, and no hook whose
// presence changes git's behaviour is chained. guard_functional_test.go runs the role through ansible-core and git.

const pushGuardRole = "roles/push-guard"

func TestPushGuardRefusesBeforeItWrites(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(pushGuardRole + "/tasks/main.yml")
	require.NoError(t, err)
	var raw []any
	require.NoError(t, yaml.Unmarshal(b, &raw))
	lastCheck, firstWrite := -1, -1
	for i, task := range flattenTasks(t, mapsOf(t, raw)) {
		if task["ansible.builtin.fail"] != nil || task["ansible.builtin.assert"] != nil || task["ansible.builtin.meta"] != nil {
			lastCheck = i
		}
		write := task["ansible.builtin.file"] != nil || task["ansible.builtin.template"] != nil || task["ansible.builtin.copy"] != nil
		if c, ok := task["ansible.builtin.command"].(map[string]any); ok && !slices.Contains(asList(c["argv"]), "--get") && !slices.Contains(asList(c["argv"]), "help") {
			write = true
		}
		if write && firstWrite < 0 {
			firstWrite = i
		}
	}
	require.GreaterOrEqual(t, firstWrite, 0)
	assert.Less(t, lastCheck, firstWrite, "a refusal comes after a write")
}

func asList(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			out = append(out, asText(x))
		}
	}
	return out
}

func TestPushGuardHookRunsTheGuardFirst(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(pushGuardRole + "/templates/pre-push.j2")
	require.NoError(t, err)
	text := string(b)
	guard := strings.Index(text, "commit-guard --rules {{ nova_push_guard_rules | quote }} --pre-push \"$@\" < \"$updates\" || exit\n")
	own := strings.Index(text, `own="$(git rev-parse --git-common-dir)/hooks/pre-push"`)
	require.GreaterOrEqual(t, guard, 0, text)
	assert.Greater(t, own, guard, "the repository's own pre-push runs after the guard")
	assert.Contains(t, text, "{{ (nova_bin_dir ~ '/nova-fleet') | quote }}", "the hook runs the installed nova-fleet by path")
	assert.Equal(t, 2, strings.Count(text, `< "$updates"`), "both hooks read git's lines")
	assert.Contains(t, text, "trap 'rm -f \"$updates\"' EXIT")

	d, err := os.ReadFile(pushGuardRole + "/defaults/main.yml")
	require.NoError(t, err)
	var defaults struct {
		Chain []string `yaml:"nova_push_guard_chain"`
	}
	require.NoError(t, yaml.Unmarshal(d, &defaults))
	assert.Contains(t, defaults.Chain, "pre-commit")
	for _, never := range []string{"pre-push", "push-to-checkout", "proc-receive"} {
		assert.NotContains(t, defaults.Chain, never)
	}
}
