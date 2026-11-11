package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// install_gh_wrapper_test.go pins the install role's removal of the v1 gh wrapper (rowan-tools
// fleet/push-credential.yml, retired with v1): a script in the bin directory that ran gh under a secrets store v2 no
// longer has, ahead of the real gh on PATH. The role removes it only when the file there names GH_PUSH_TOKEN, removes
// the git credential helper entries that name it, and reads only the helpers' names (a helper line can hold a token).
// install_gh_wrapper_functional_test.go runs the same tasks through ansible-core.

func installTasks(t *testing.T) []map[string]any {
	t.Helper()
	b, err := os.ReadFile("roles/nova-tools-install/tasks/main.yml")
	require.NoError(t, err)
	var raw []any
	require.NoError(t, yaml.Unmarshal(b, &raw))
	return flattenTasks(t, mapsOf(t, raw))
}

func TestInstallRemovesOnlyTheV1GhWrapper(t *testing.T) {
	t.Parallel()
	var removes, reads, unsets []map[string]any
	for _, task := range installTasks(t) {
		text := taskText(t, task)
		switch {
		case task["ansible.builtin.file"] != nil && strings.Contains(text, "/gh") && strings.Contains(text, "absent"):
			removes = append(removes, task)
		case task["ansible.builtin.command"] != nil && strings.Contains(text, "credential") && strings.Contains(text, "--name-only"):
			reads = append(reads, task)
		case task["ansible.builtin.command"] != nil && strings.Contains(text, "--unset-all"):
			unsets = append(unsets, task)
		}
		assert.NotContains(t, text, "GH-WRAPPER KEPT", "the wrapper is never kept")
	}
	require.Len(t, removes, 1, "one task removes the bin directory's gh")
	assert.Contains(t, asText(removes[0]["when"]), "nova_tools_gh_grep", "it is removed only when it is the v1 wrapper")
	require.Len(t, reads, 1, "the git credential helpers are read once")
	assert.Equal(t, false, reads[0]["check_mode"], "a read: --check runs it too")
	require.Len(t, unsets, 1, "the helper entries that name the wrapper are unset")
	assert.Contains(t, taskText(t, unsets[0]), "regex_escape", "only the entries naming the wrapper's path go")
	assert.Contains(t, asText(unsets[0]["loop"]), "nova_tools_gh_helpers.stdout_lines", "by the names the read found")
}
