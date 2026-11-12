//go:build functional

package main

import (
	"cmp"
	"context"
	"maps"
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
// wrote no users.acl, a missing sops at join, a runner whose labels lack a wanted one.

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
	// the github-runner role's main.yml sets whose runners these are before any of its task files runs: the fleet
	// login's (this one), unless a test says otherwise
	me, err := user.Current()
	require.NoError(t, err)
	all := map[string]any{"github_runner_other": false, "github_runner_user": me.Username}
	maps.Copy(all, vars)
	play := []map[string]any{{"hosts": "localhost", "connection": "local", "gather_facts": true, "gather_subset": []string{"min"}, "vars": all, "tasks": tasks}}
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

// TestFunctionalRunnerGetsTheLabelsItLacks: a registered runner whose record (.labels) lacks a wanted label, or that has
// no record (registered before the file, or the file lost), gets the label added in GitHub by the control node's gh
// (a fake here that answers the lookup and logs the POST and its stdin body), and the record is rewritten from what
// GitHub holds; a label held beyond the wanted is reported and kept; --check and a runner not registered yet call
// nothing; a runner GitHub does not list fails the run, its labels untouched.
func TestFunctionalRunnerGetsTheLabelsItLacks(t *testing.T) {
	t.Parallel()
	tasks := taskRange(t, "roles/github-runner/tasks/unix-runner.yml", "the labels it holds now", "the unit (Linux)")
	for name, c := range map[string]struct {
		labels, github string // the record's labels; the custom labels GitHub holds ("" lists no runner)
		need           []any
		check, fails   bool
		post, record   string // the POST body sent ("" none); the record after ("" unchanged)
		want, out      string // the wanted labels ("" is a,b); a line the run prints
	}{
		"GitHub's Linux is linux":     {labels: "a\n", github: "A", want: "a,linux", need: []any{}, record: "a,linux\n", out: "LABEL-ADD r1 added=- labels=a,linux"},
		"an empty record":             {labels: "\n", github: "a,b", need: []any{}, record: "a,b\n", out: "LABEL-ADD r1 added=- labels=a,b"},
		"same labels":                 {labels: "a,b\n", github: "a,b", need: []any{}, out: "ok="},
		"lacks one":                   {labels: "a\n", github: "a", need: []any{}, post: `{"labels": ["b"]}`, record: "a,b\n", out: "LABEL-ADD r1 added=b labels=a,b"},
		"no record":                   {labels: "", github: "a,c", need: []any{}, post: `{"labels": ["b"]}`, record: "a,b,c\n", out: "LABEL-ADD r1 added=b labels=a,b,c"},
		"no record, GitHub has all":   {labels: "", github: "a,b", need: []any{}, record: "a,b\n", out: "LABEL-ADD r1 added=- labels=a,b"},
		"an extra label":              {labels: "a,b,c\n", github: "a,b,c", need: []any{}, out: "LABEL-EXTRA r1: holds c beyond the wanted a,b; kept"},
		"check, lacks one":            {labels: "a\n", github: "a", need: []any{}, check: true, out: "LABEL-PLAN r1: lacks b"},
		"check, not registered yet":   {labels: "", github: "", need: []any{0}, check: true, out: "ok="},
		"GitHub lists no such runner": {labels: "a\n", github: "", need: []any{}, fails: true, out: "GitHub lists no runner of that name"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if c.labels != "" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, ".labels"), []byte(c.labels), 0o644))
			}
			entry := `{"total_count":0,"runners":[]}`
			if c.github != "" {
				var ls []string
				for _, l := range strings.Split(c.github, ",") {
					ls = append(ls, `{"id":1,"name":"`+l+`","type":"custom"}`)
				}
				entry = `{"total_count":1,"runners":[{"id":42,"name":"r1","labels":[{"id":0,"name":"self-hosted","type":"read-only"},{"id":0,"name":"Linux","type":"read-only"},` + strings.Join(ls, ",") + `]}]}`
			}
			calls := filepath.Join(dir, "gh.calls")
			gh := `echo "$*" >> ` + calls + `
case "$*" in
  "api -X POST repos/o/r/actions/runners/42/labels --input -") cat >> ` + calls + `; echo '{}' ;;
  "api repos/o/r/actions/runners?name=r1") echo '` + entry + `' ;;
  *) echo "unexpected: $*" >&2; exit 9 ;;
esac`
			var extra []string
			if c.check {
				extra = []string{"--check"}
			}
			out, err := runTasksLocally(t, tasks, map[string]any{
				"github_runner_dir": dir, "github_runner_name": "r1", "github_runner_label_string": cmp.Or(c.want, "a,b"), "runner_index": 0, "github_runner_need": c.need,
				"github_runner_gh": "gh", "github_runner_scope": "repo", "github_runner_path": "o/r",
			}, map[string]string{"gh": gh}, extra...)
			if c.fails {
				require.Error(t, err, out)
			} else {
				require.NoError(t, err, out)
			}
			assert.Contains(t, out, c.out)
			got, _ := os.ReadFile(calls) // ignored: absent when gh was never called
			if c.post != "" {
				assert.Contains(t, string(got), "api -X POST repos/o/r/actions/runners/42/labels --input -\n"+c.post, "the body rides on stdin")
			} else {
				assert.NotContains(t, string(got), "POST", "no label was added")
			}
			if c.check || c.out == "ok=" {
				assert.Empty(t, string(got), "gh was called")
			}
			record, _ := os.ReadFile(filepath.Join(dir, ".labels")) // ignored: compared below
			want := c.labels
			if c.record != "" {
				want = c.record
			}
			assert.Equal(t, want, string(record))
		})
	}
}
