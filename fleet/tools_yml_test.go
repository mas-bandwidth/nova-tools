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
// own install, and fleet/retired-tools.txt never names it. The coordinator
// installs inside the seat play's window, not the install play, so the seat
// play's release install must be followed by the same copy: install replaces a
// tool whose bytes differ from the release artifact (internal/release/install.go,
// security#72 finding 2), so a copy made before that install would be undone.
func TestToolsYmlBuildsNovaSprintFromItsOwnRepo(t *testing.T) {
	t.Parallel()
	var vars map[string]any
	require.NoError(t, yaml.Unmarshal(readFleetFile(t, "group_vars", "all.yml"), &vars))
	var plays []map[string]any
	require.NoError(t, yaml.Unmarshal(readFleetFile(t, "tools.yml"), &plays))
	buildPlay := playNamed(t, plays, "the build, once per platform")
	installPlay := playNamed(t, plays, "the build in ~/.local/bin on every machine")
	seatPlay := playNamed(t, plays, "the seat adopts the build")
	build := playTasks(t, buildPlay)
	install := playTasks(t, installPlay)
	seat := flattenTasks(t, playTasks(t, seatPlay))
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
		return str(cp["src"]) == "{{ nova_sprint_out }}/{{ nova_platform }}/{{ tools_sprint_file }}" &&
			str(cp["dest"]) == "{{ nova_bin_dir }}/{{ tools_sprint_file }}" && cp["mode"] == "0755"
	})
	seatRelease := taskIndex(seat, func(task map[string]any) bool {
		cmd, _ := task["ansible.builtin.command"].(map[string]any)
		return slices.Contains(stringList(cmd["argv"]), "install")
	})
	seatCopied := taskIndex(seat, func(task map[string]any) bool {
		cp, _ := task["ansible.builtin.copy"].(map[string]any)
		return str(cp["src"]) == "{{ nova_sprint_out }}/{{ nova_platform }}/{{ tools_sprint_file }}" &&
			str(cp["dest"]) == "{{ nova_bin_dir }}/{{ tools_sprint_file }}" && cp["mode"] == "0755"
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
		{"the install play's release install sends coordinators to the seat window", excludesCoordinators(install[release])},
		{"the install play's copy sends coordinators to the seat window", excludesCoordinators(install[copied])},
		{"the install play's copy still waits out check mode", strings.Contains(strings.Join(whenLines(install[copied]), " "), "not ansible_check_mode")},
		{"the seat play installs through the release's own install", seatRelease >= 0},
		{"the seat play places nova-sprint after that install", seatCopied > seatRelease && seatRelease >= 0},
		{"the seat window's copy is the one that installs on the coordinator", !excludesCoordinators(seat[seatCopied])},
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
	entries, ok := play["tasks"].([]any)
	require.True(t, ok, "play has no task list")
	for _, e := range entries {
		task, ok := e.(map[string]any)
		require.True(t, ok, "play task is not a mapping")
		tasks = append(tasks, task)
	}
	return tasks
}

// taskIndex is the index of the first task match accepts, or -1.
func taskIndex(tasks []map[string]any, match func(map[string]any) bool) int {
	return slices.IndexFunc(tasks, match)
}

// whenLines is a task's when as written: the scalar form as one line, the list
// form as its lines, and no condition as none.
func whenLines(task map[string]any) []string {
	switch w := task["when"].(type) {
	case string:
		return []string{w}
	case []any:
		var out []string
		for _, e := range w {
			out = append(out, str(e))
		}
		return out
	case nil:
		return nil
	default:
		return []string{str(w)}
	}
}

// excludesCoordinators is true when a task's when keeps the coordinator out of
// it: the coordinator installs inside the seat play's window, not the install
// play (docs/FLEET.md, "tools.yml", step 3), so the install play's copy leaves
// the coordinator's nova-sprint to the guarded copy in that window.
func excludesCoordinators(task map[string]any) bool {
	return slices.ContainsFunc(whenLines(task), func(line string) bool {
		return strings.Contains(line, "inventory_hostname not in") && strings.Contains(line, "coordinator")
	})
}

// flattenTasks splices every task's nested block in at the task's place, so
// document order survives for tasks a play nests: the seat play's release
// install and the nova-sprint copy that must follow it are both inside the
// window block.
func flattenTasks(t *testing.T, tasks []map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, task := range tasks {
		out = append(out, task)
		entries, ok := task["block"].([]any)
		if !ok {
			continue
		}
		var nested []map[string]any
		for _, e := range entries {
			m, ok := e.(map[string]any)
			require.True(t, ok, "a nested block task is not a mapping")
			nested = append(nested, m)
		}
		out = append(out, flattenTasks(t, nested)...)
	}
	return out
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
