//go:build functional

package main

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// play_failures_functional_test.go runs a few of the plays' and roles' own tasks, copied into a localhost playbook,
// against the test's temp dir, with a fake systemctl or launchctl first on PATH where a task calls one. Each task used
// to report success over a failure: an empty Redis password file that
// wrote no users.acl, a missing sops at join, a runner with no record of its labels.

// taskGroups is a playbook's tasks, one group per play, or a role task file's tasks as one group.
func taskGroups(t *testing.T, file string) [][]map[string]any {
	t.Helper()
	b, err := os.ReadFile(file)
	require.NoError(t, err)
	var top []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &top))
	var groups [][]map[string]any
	for _, m := range top {
		if m["tasks"] != nil {
			groups = append(groups, mapsOf(t, m["tasks"]))
		}
	}
	if groups == nil {
		groups = [][]map[string]any{top}
	}
	return groups
}

// taskRange is the tasks from the one named first up to, not including, the one named last (to the end of its play
// when last is empty).
func taskRange(t *testing.T, file, first, last string) []map[string]any {
	t.Helper()
	for _, tasks := range taskGroups(t, file) {
		from := taskIndex(tasks, first)
		if from < 0 {
			continue
		}
		if last == "" {
			return tasks[from:]
		}
		if to := taskIndex(tasks[from+1:], last); to >= 0 {
			return tasks[from : from+1+to]
		}
	}
	t.Fatalf("%s: no tasks from %q to %q", file, first, last)
	return nil
}

// runTasksLocally runs tasks on localhost with vars, a temp dir's fakes first on PATH, and ansible-playbook's extra
// arguments; it returns the output.
func runTasksLocally(t *testing.T, tasks []map[string]any, vars map[string]any, fakes map[string]string, extra ...string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	for name, script := range fakes {
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755))
	}
	play := []map[string]any{{"hosts": "localhost", "connection": "local", "gather_facts": true, "gather_subset": []string{"min"}, "vars": vars, "tasks": tasks}}
	b, err := yaml.Marshal(play)
	require.NoError(t, err)
	file := filepath.Join(dir, "play.yml")
	require.NoError(t, os.WriteFile(file, b, 0o644))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ansible-playbook", append(append([]string{"-i", "localhost,"}, extra...), file)...)
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "ANSIBLE_LOCAL_TEMP="+filepath.Join(dir, "tmp"), "PATH="+bin+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestFunctionalRedisRefusesAnEmptyPasswordFile: an empty <user>.pass (kept by creates:) fails a run naming the file;
// only --check, before the passwords exist, reports changed for it.
func TestFunctionalRedisRefusesAnEmptyPasswordFile(t *testing.T) {
	t.Parallel()
	tasks := taskRange(t, "roles/redis/tasks/acl.yml", "users.acl from the hashes", "users.acl is pending")
	me, err := user.Current()
	require.NoError(t, err)
	group, err := user.LookupGroupId(me.Gid)
	require.NoError(t, err)
	for name, c := range map[string]struct {
		check bool
		fails bool
	}{"run": {false, true}, "check": {true, false}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "bus.pass"), nil, 0o600))
			var extra []string
			if c.check {
				extra = []string{"--check"}
			}
			out, err := runTasksLocally(t, tasks, map[string]any{
				"nova_redis_dir": dir, "nova_redis_users": []any{map[string]any{"name": "bus", "rules": "~bus:* +@all"}},
				"nova_redis_owner": me.Username, "nova_redis_group": group.Name, "redis_acl_default": "off",
			}, nil, extra...)
			if c.fails {
				require.Error(t, err, out)
				assert.Contains(t, out, "bus.pass")
				return
			}
			require.NoError(t, err, out)
			assert.Contains(t, out, "changed: [localhost]")
		})
	}
}

// TestFunctionalJoinRefusesAMissingSops: when sops does not answer, the tools' step stops the join with the fix,
// instead of printing sops= and going on to the seat.
func TestFunctionalJoinRefusesAMissingSops(t *testing.T) {
	t.Parallel()
	tasks := taskRange(t, "join.yml", "age-keygen and sops answer", "")
	for name, c := range map[string]struct {
		sops  string
		fails bool
	}{"missing": {"", true}, "present": {"echo 'sops 3.9.4 (latest)'", false}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fakes := map[string]string{"age-keygen-fake": "echo v1.2.1"}
			sops := "/nonexistent/sops"
			if c.sops != "" {
				fakes["sops-fake"] = c.sops
				sops = "sops-fake"
			}
			out, err := runTasksLocally(t, tasks, map[string]any{"nova_age_keygen": "age-keygen-fake", "nova_sops": sops, "nova_system": "Linux"}, fakes)
			if c.fails {
				require.Error(t, err, out)
				assert.Contains(t, out, "sops")
				assert.Contains(t, out, "run: nova-fleet join localhost")
				return
			}
			require.NoError(t, err, out)
			assert.Contains(t, out, "sops=3.9.4")
		})
	}
}

// TestFunctionalRunnerReportsLabelsItCannotRead: a runner with no .labels (registered before the file, or the file
// lost) is reported as label drift, since its labels cannot be checked; --check on a runner not registered yet is not.
func TestFunctionalRunnerReportsLabelsItCannotRead(t *testing.T) {
	t.Parallel()
	tasks := taskRange(t, "roles/github-runner/tasks/unix-runner.yml", "the labels it holds now", "the unit (Linux)")
	for name, c := range map[string]struct {
		labels string
		need   []any
		check  bool
		drift  bool
	}{
		"no labels file":            {"", []any{}, false, true},
		"same labels":               {"a,b\n", []any{}, false, false},
		"other labels":              {"a\n", []any{}, false, true},
		"check, not registered yet": {"", []any{0}, true, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if c.labels != "" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, ".labels"), []byte(c.labels), 0o644))
			}
			var extra []string
			if c.check {
				extra = []string{"--check"}
			}
			out, err := runTasksLocally(t, tasks, map[string]any{
				"github_runner_dir": dir, "github_runner_name": "r1", "github_runner_label_string": "a,b", "runner_index": 0, "github_runner_need": c.need,
			}, nil, extra...)
			require.NoError(t, err, out)
			assert.Equal(t, c.drift, strings.Contains(out, "LABEL-DRIFT r1"), out)
		})
	}
}
