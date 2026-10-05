package fleet

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestToolsYmlBuildsNovaSprintFromItsOwnRepo pins the row of tools.yml that
// builds nova-sprint (docs/FLEET.md, "tools.yml", steps 2 and 3): nova-sprint
// is its own product (nova-tools PR 5309), so the play checks out
// nova_sprint_repo at nova_sprint_ref on the machine running it and builds
// ./cmd/nova-sprint there for every platform, stamped with the build's
// version, instead of taking it from the nova-tools checkout's cmd/; every
// machine then holds it in the bin directory by its name, after the release's
// own install, and fleet/retired-tools.txt never names it.
func TestToolsYmlBuildsNovaSprintFromItsOwnRepo(t *testing.T) {
	t.Parallel()
	var vars map[string]any
	require.NoError(t, yaml.Unmarshal(readFleetFile(t, "group_vars", "all.yml"), &vars))
	var plays []map[string]any
	require.NoError(t, yaml.Unmarshal(readFleetFile(t, "tools.yml"), &plays))
	buildPlay := playNamed(t, plays, "the build, once per platform")
	installPlay := playNamed(t, plays, "the build in ~/.local/bin on every machine")
	build := playTasks(t, buildPlay)
	install := playTasks(t, installPlay)
	installVars, _ := installPlay["vars"].(map[string]any)
	sprintFile := str(installVars["tools_sprint_file"])

	checkout := taskIndex(build, func(task map[string]any) bool {
		git, _ := task["ansible.builtin.git"].(map[string]any)
		return git["repo"] == "{{ nova_sprint_repo }}" && git["version"] == "{{ nova_sprint_ref }}" && git["dest"] == "{{ nova_sprint_src }}"
	})
	compile := taskIndex(build, func(task map[string]any) bool {
		cmd, _ := task["ansible.builtin.command"].(map[string]any)
		argv := strings.Join(stringList(cmd["argv"]), " ")
		env, _ := task["environment"].(map[string]any)
		return cmd["chdir"] == "{{ nova_sprint_src }}" && strings.Contains(argv, "nice -n 19 go build") &&
			strings.Contains(argv, "go build -trimpath -ldflags -s -w -X main.version={{ nova_version }} -o ") && strings.HasSuffix(argv, "./cmd/nova-sprint") &&
			strings.Contains(argv, "-o {{ nova_sprint_out }}/{{ item }}/nova-sprint{{ '.exe' if item.startswith('windows-') else '' }} ./cmd/nova-sprint") && env["CGO_ENABLED"] == "0"
	})
	release := taskIndex(install, func(task map[string]any) bool {
		cmd, _ := task["ansible.builtin.command"].(map[string]any)
		return slices.Contains(stringList(cmd["argv"]), "install")
	})
	copied := taskIndex(install, func(task map[string]any) bool {
		cp, _ := task["ansible.builtin.copy"].(map[string]any)
		return str(cp["src"]) == "{{ nova_sprint_out }}/{{ nova_platform }}/"+sprintFile &&
			str(cp["dest"]) == "{{ nova_bin_dir }}/"+sprintFile && cp["mode"] == "0755"
	})

	cases := []struct {
		name string
		ok   bool
	}{
		{"nova_sprint_repo is mas-bandwidth/nova-sprint", vars["nova_sprint_repo"] == "https://github.com/mas-bandwidth/nova-sprint.git"},
		{"nova_sprint_ref is main", vars["nova_sprint_ref"] == "main"},
		{"the installed file uses the platform executable name", sprintFile == "nova-sprint{{ '.exe' if nova_platform.startswith('windows-') else '' }}"},
		{"the checkout and the builds live under nova_release_out", strings.HasPrefix(str(vars["nova_sprint_src"]), "{{ nova_release_out }}/") &&
			strings.HasPrefix(str(vars["nova_sprint_out"]), "{{ nova_release_out }}/")},
		{"the build play checks out the nova-sprint repository", checkout >= 0},
		{"the build play builds ./cmd/nova-sprint in that checkout, after it", compile > checkout && checkout >= 0},
		{"the install play copies nova-sprint into the bin directory by name", copied >= 0},
		{"after the release's own install", copied > release && release >= 0},
		{"fleet/retired-tools.txt never names nova-sprint", !slices.Contains(strings.Fields(string(readFleetFile(t, "retired-tools.txt"))), "nova-sprint")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.True(t, tc.ok)
		})
	}
}

func readFleetFile(t *testing.T, path ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(path...))
	require.NoError(t, err)
	return b
}

func playNamed(t *testing.T, plays []map[string]any, name string) map[string]any {
	t.Helper()
	for _, p := range plays {
		if p["name"] == name {
			return p
		}
	}
	require.Failf(t, "no play", "tools.yml has no play named %q", name)
	return nil
}

func playTasks(t *testing.T, play map[string]any) []map[string]any {
	t.Helper()
	var tasks []map[string]any
	for _, e := range play["tasks"].([]any) {
		tasks = append(tasks, e.(map[string]any))
	}
	return tasks
}

// taskIndex is the index of the first task match accepts, or -1.
func taskIndex(tasks []map[string]any, match func(map[string]any) bool) int {
	return slices.IndexFunc(tasks, match)
}

func stringList(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, e := range l {
		out = append(out, str(e))
	}
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
