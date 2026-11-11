package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// github_runner_defaults_test.go pins three defaults of the github-runner role (cmd/nova-fleet/README.md
// "github-runners"): every Linux runner's service starts with a TMPDIR of the login's, which the play creates; every
// runner is labelled fleet, as the Windows side is, and a registered runner lacking a label gets it in GitHub without
// re-registering; and the service's environment is the machine record's runner_env, read from the inventory, so a run
// without -e keeps it. The render through ansible's templar is github_runner_path_functional_test.go; the relabel run
// is TestFunctionalRunnerGetsTheLabelsItLacks.

func roleTaskFile(t *testing.T, name string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile("roles/github-runner/tasks/" + name)
	require.NoError(t, err)
	var tasks []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &tasks))
	return tasks
}

func runnerTask(t *testing.T, tasks []map[string]any, name string) map[string]any {
	t.Helper()
	for _, task := range tasks {
		if task["name"] == name {
			return task
		}
	}
	t.Fatalf("no task named %q", name)
	return nil
}

// The environment is the record's: nova-config machine set <machine> --runner_env, the inventory's nova_runner_env.
func TestRunnerEnvDefaultsToTheMachineRecord(t *testing.T) {
	t.Parallel()
	d := runnerDefaults(t)
	assert.Equal(t, "{{ nova_runner_env | default({}) }}", asText(d["github_runner_env"]))
}

// The Linux unit sets TMPDIR once: github_runner_env's TMPDIR if it has one, else github_runner_tmpdir, one directory
// of the login that the play creates (0700, as the login) before the units start. The macOS daemon writes a TMPDIR only
// from github_runner_env: without one its jobs use /tmp, which macOS cleans, and the health play cleans no runner TMPDIR.
func TestRunnerServiceFilesSetTmpdir(t *testing.T) {
	t.Parallel()
	d := runnerDefaults(t)
	assert.Equal(t, "{{ ansible_facts['user_dir'] }}/.cache/runner-tmp", asText(d["github_runner_tmpdir"]))
	b, err := os.ReadFile("roles/github-runner/templates/runner.service.j2")
	require.NoError(t, err)
	service := string(b)
	assert.Contains(t, service, "\nEnvironment=TMPDIR={{ github_runner_env.get('TMPDIR', github_runner_tmpdir) }}\n")
	assert.Equal(t, 1, strings.Count(service, "TMPDIR="), "TMPDIR is set once")
	assert.Contains(t, service, "github_runner_env.items() | sort if k not in ['PATH', 'TMPDIR']", "the env loop writes PATH or TMPDIR a second time")
	b, err = os.ReadFile("roles/github-runner/templates/runner.plist.j2")
	require.NoError(t, err)
	assert.NotContains(t, string(b), "github_runner_tmpdir", "the macOS daemon sets no TMPDIR of its own")

	task := runnerTask(t, roleTaskFile(t, "unix.yml"), "the runners' TMPDIR")
	file, _ := task["ansible.builtin.file"].(map[string]any)
	assert.Equal(t, "{{ github_runner_env.get('TMPDIR', github_runner_tmpdir) }}", file["path"])
	assert.Equal(t, "directory", file["state"])
	assert.Equal(t, "0700", file["mode"])
	assert.Equal(t, "{{ ansible_facts['user_id'] }}", file["owner"])
	assert.Nil(t, task["become"], "the directory is the login's, made as the login")
	assert.Contains(t, asText(task["when"]), "ansible_facts['system'] == 'Linux' or 'TMPDIR' in github_runner_env")
	tasks := roleTaskFile(t, "unix.yml")
	assert.Less(t, taskIndex(tasks, "the runners' TMPDIR"), taskIndex(tasks, "each wanted runner"), "made before any unit starts")
}

// Every runner is labelled fleet before the machine's roles, os and arch: one definition for Linux, macOS and the Windows
// side of a WSL machine.
func TestRunnersAreLabelledFleet(t *testing.T) {
	t.Parallel()
	d := runnerDefaults(t)
	assert.Equal(t, []any{"fleet"}, d["github_runner_base_labels"])
	assert.Equal(t, "{{ github_runner_base_labels + [inventory_hostname] }}", asText(d["github_runner_wsl_labels"]))
	task := runnerTask(t, roleTaskFile(t, "scope.yml"), "the labels")
	assert.Contains(t, asText(task["ansible.builtin.set_fact"].(map[string]any)["github_runner_label_list"]), "github_runner_base_labels")
}

// A registered runner whose labels lack a wanted one gets it added in GitHub by the control node's gh (POST
// .../actions/runners/<id>/labels, the body on stdin): no re-registration, so the runner keeps its session and the jobs
// queued for it. Nothing in the relabel runs config.sh, and --check calls nothing.
func TestRunnerLabelsAreAddedWithoutReregistering(t *testing.T) {
	t.Parallel()
	tasks := roleTaskFile(t, "unix-runner.yml")
	block := runnerTask(t, tasks, "a registered runner that lacks a wanted label gets it in GitHub")
	assert.Contains(t, asText(block["when"]), "not ansible_check_mode")
	assert.Contains(t, asText(block["when"]), "runner_index not in github_runner_need", "a runner registered in this run has its labels")
	inner := mapsOf(t, block["block"])
	add := runnerTask(t, inner, "the labels it lacks, added in GitHub")
	cmd, _ := add["ansible.builtin.command"].(map[string]any)
	argv := strings.Join(func() []string {
		var a []string
		for _, v := range cmd["argv"].([]any) {
			a = append(a, asText(v))
		}
		return a
	}(), " ")
	assert.Contains(t, argv, "api -X POST ")
	assert.Contains(t, argv, "/actions/runners/{{ github_runner_gh_entry[0].id }}/labels --input -")
	assert.Contains(t, asText(cmd["stdin"]), "to_json", "the body rides on stdin")
	assert.Equal(t, "localhost", add["delegate_to"])
	assert.Equal(t, false, add["become"])
	runnerTask(t, inner, "the labels it holds now, recorded")
	for _, task := range append(tasks, inner...) {
		b, err := yaml.Marshal(task)
		require.NoError(t, err)
		assert.NotContains(t, string(b), "./config.sh", "unix-runner.yml never runs config.sh itself (unix-register.yml does, once)")
	}
	assert.Equal(t, -1, taskIndex(tasks, "label drift"), "a missing label is added, never only reported")
}
