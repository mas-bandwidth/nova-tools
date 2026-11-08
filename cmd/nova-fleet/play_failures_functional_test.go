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
// to report success over a failure: a unit moved aside that was never stopped, an empty Redis password file that
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

// TestFunctionalInstallMovesARetiredUnitOnlyOnceStopped: a retired unit that systemctl or launchctl could not stop
// stays where it is and the run fails with the cause; one that stopped, or that launchd does not have loaded, is
// moved into retired-<date>/.
func TestFunctionalInstallMovesARetiredUnitOnlyOnceStopped(t *testing.T) {
	t.Parallel()
	tasks := taskRange(t, "roles/nova-tools-install/tasks/main.yml", "the retired units, stopped and moved aside", "this release's names")
	for name, c := range map[string]struct {
		os, fake, script string
		moved            bool
	}{
		"systemctl fails":           {"linux", "systemctl", "echo 'Failed to connect to bus: No medium found' >&2; exit 1", false},
		"systemctl stops it":        {"linux", "systemctl", `echo "$*" >> "$CALLS"`, true},
		"launchctl fails, loaded":   {"darwin", "launchctl", `case "$1" in bootout) echo 'Boot-out failed: 5: Input/output error' >&2; exit 5;; *) exit 0;; esac`, false},
		"launchctl fails, unloaded": {"darwin", "launchctl", `case "$1" in bootout) echo 'Boot-out failed: 3: No such process' >&2; exit 3;; *) exit 113;; esac`, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			units, sys := t.TempDir(), t.TempDir()
			unit := filepath.Join(units, "nova-old.service")
			require.NoError(t, os.WriteFile(unit, []byte("[Unit]\n"), 0o644))
			out, err := runTasksLocally(t, tasks, map[string]any{
				"nova_tools_unit_manager": true, "nova_tools_os": c.os, "nova_tools_remove_enabled": true, "nova_tools_win": false,
				"nova_tools_system_unit_dir": sys, "nova_tools_user_unit_dir": units,
				"nova_tools_unit_stat": map[string]any{"results": []any{map[string]any{"stat": map[string]any{"exists": true}, "item": []any{units, "nova-old.service"}}}},
			}, map[string]string{c.fake: "CALLS=" + filepath.Join(sys, "calls") + "\n" + c.script})
			_, stat := os.Stat(unit)
			if c.moved {
				require.NoError(t, err, out)
				assert.True(t, os.IsNotExist(stat), "the stopped unit is moved aside")
				if c.os == "linux" {
					calls, err := os.ReadFile(filepath.Join(sys, "calls"))
					require.NoError(t, err)
					assert.Equal(t, "--user disable --now nova-old.service\n--user daemon-reload\n", string(calls), "systemd reads the user directory again, and only it")
				}
				return
			}
			require.Error(t, err, out)
			assert.NoError(t, stat, "a unit still running stays where it is")
			assert.Regexp(t, `"stderr": "(Failed to connect to bus|Boot-out failed: 5)`, out, "the refusal carries the cause")
		})
	}
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
