package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// github_runner_path_test.go pins the PATH a runner's service starts with (roles/github-runner, cmd/nova-fleet/README.md
// "github-runners"). Its jobs inherit it, so a workflow step that runs a nova tool (`nova-reserve run ...`) finds it
// only when the directory install.yml puts the tools in is on it. Until 2026-11-08 the Linux unit set no PATH and the
// macOS daemon's default lacked it: `nova-reserve: command not found` (exit 127) on every bench runner, with the
// binary installed. The render through ansible's templar is github_runner_path_functional_test.go.

func runnerDefaults(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("roles/github-runner/defaults/main.yml")
	require.NoError(t, err)
	var d map[string]any
	require.NoError(t, yaml.Unmarshal(b, &d))
	return d
}

// The tools' directory is one definition, nova_bin_dir in group_vars/all.yml, which every fleet play reads: the install
// role installs there and the runner role puts it first on the runner's PATH, never a second copy of the rule.
func TestRunnerPathStartsWithTheToolsDir(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("group_vars/all.yml")
	require.NoError(t, err)
	var all map[string]any
	require.NoError(t, yaml.Unmarshal(b, &all))
	require.Contains(t, all, "nova_bin_dir")
	b, err = os.ReadFile("roles/nova-tools-install/defaults/main.yml")
	require.NoError(t, err)
	var install map[string]any
	require.NoError(t, yaml.Unmarshal(b, &install))
	assert.True(t, strings.HasPrefix(asText(install["nova_tools_bin_dir"]), "{{ nova_bin_dir "), "the install role's bin dir is nova_bin_dir")

	d := runnerDefaults(t)
	assert.Equal(t, "{{ nova_bin_dir }}:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/usr/games:/usr/local/games:/snap/bin",
		asText(d["github_runner_default_path_linux"]))
	assert.Equal(t, "{{ nova_bin_dir }}:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin", asText(d["github_runner_default_path_darwin"]))
}

// Both service files set PATH once: github_runner_env's PATH if it has one, else the default above.
func TestRunnerServiceFilesSetPath(t *testing.T) {
	t.Parallel()
	for f, want := range map[string]string{
		"roles/github-runner/templates/runner.service.j2": "Environment=PATH={{ github_runner_env.get('PATH', github_runner_default_path_linux) }}\n",
		"roles/github-runner/templates/runner.plist.j2":   "<string>{{ github_runner_env.get('PATH', github_runner_default_path_darwin) }}</string>\n",
	} {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		assert.Contains(t, string(b), want, f)
		assert.Contains(t, string(b), "github_runner_env.items() | sort if k != 'PATH'", f+": the env loop writes PATH a second time")
	}
}
