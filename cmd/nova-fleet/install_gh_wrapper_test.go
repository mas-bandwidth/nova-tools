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
// longer has, ahead of the real gh on PATH. The role removes it only when the file there names GH_PUSH_TOKEN, keeps it
// when a git credential helper still names it, and reads the helpers with no_log (a helper line can hold a token).
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
	var removes, keeps, helpers []map[string]any
	for _, task := range installTasks(t) {
		text := taskText(t, task)
		switch {
		case task["ansible.builtin.file"] != nil && strings.Contains(text, "/gh") && strings.Contains(text, "absent"):
			removes = append(removes, task)
		case task["ansible.builtin.debug"] != nil && strings.Contains(text, "GH-WRAPPER KEPT"):
			keeps = append(keeps, task)
		case task["ansible.builtin.command"] != nil && strings.Contains(text, "credential"):
			helpers = append(helpers, task)
		}
	}
	require.Len(t, removes, 1, "one task removes the bin directory's gh")
	when := asText(removes[0]["when"])
	assert.Contains(t, when, "nova_tools_gh_wrapper", "it is removed only when it is the v1 wrapper")
	assert.Contains(t, when, "not nova_tools_gh_named", "it is kept while a git credential helper names it")
	require.Len(t, keeps, 1, "a kept wrapper is reported")
	assert.Contains(t, asText(keeps[0]["when"]), "nova_tools_gh_named")
	require.Len(t, helpers, 1, "the git credential helpers are read once")
	assert.Equal(t, true, helpers[0]["no_log"], "a helper line can hold a token: never logged")
	assert.Equal(t, false, helpers[0]["check_mode"], "a read: --check runs it too")
}
