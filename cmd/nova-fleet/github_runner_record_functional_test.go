//go:build functional

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// github_runner_record_functional_test.go runs the github-runner role's record tasks (tasks/record.yml and the files it
// includes) on localhost against the test's temp dir, with fakes for gh and pgrep: which runners on a host the record
// does not want, the refusal of a record that names runners but no scopes, a stray's url read from its .runner, the
// GitHub entries of the host that are deleted (offline and not wanted) and left (online), and the wait for a running job.

var recordScopes = []any{
	map[string]any{"url": "https://github.com/o/r", "count": 2, "labels": []any{}},
	map[string]any{"url": "https://github.com/org", "count": 1, "labels": []any{"x"}},
}

// TestFunctionalRecordFindsTheRunnersItDoesNotWant: a directory or a service named <slug>-<i> that no scope wants is a
// stray (above a scope's count, of a url the record does not name, a service with no directory); the wanted ones, the
// cache directory and a service's drop-in directory are not.
func TestFunctionalRecordFindsTheRunnersItDoesNotWant(t *testing.T) {
	t.Parallel()
	base, units := t.TempDir(), t.TempDir()
	for _, d := range []string{"o-r-1", "o-r-2", "o-r-3", "org-1", "x-y-1", ".cache"} {
		require.NoError(t, os.MkdirAll(filepath.Join(base, d), 0o755))
	}
	for _, f := range []string{"github-runner-o-r-1.service", "github-runner.o-r-1.plist", "github-runner-z-1.service", "github-runner.z-1.plist", "other.service"} {
		require.NoError(t, os.WriteFile(filepath.Join(units, f), nil, 0o644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(units, "github-runner-q-1.service.d"), 0o755))
	tasks := append(taskRange(t, "roles/github-runner/tasks/record.yml", "the runners the record wants", "retire each runner"),
		map[string]any{"name": "print", "ansible.builtin.debug": map[string]any{"msg": "STRAYS={{ github_runner_record_strays | join(',') }} WANT={{ github_runner_record_want | join(',') }}"}})
	out, err := runTasksLocally(t, tasks, map[string]any{"nova_runner_scopes": recordScopes, "github_runner_base_dir": base, "github_runner_unit_dir": units}, nil)
	require.NoError(t, err, out)
	assert.Contains(t, out, "STRAYS=o-r-3,x-y-1,z-1 WANT=o-r-1,o-r-2,org-1")
}

// A record with runners above 0 and no runner_scopes is refused (it would read as none and retire them); runners 0 and
// no scopes is a host with no runners.
func TestFunctionalRecordRefusesRunnersWithoutScopes(t *testing.T) {
	t.Parallel()
	tasks := taskRange(t, "roles/github-runner/tasks/record.yml", "the record names this host's runners", "the runners the record wants")
	out, err := runTasksLocally(t, tasks, map[string]any{"runners": 2, "github_runner_state": "present"}, nil)
	require.Error(t, err, out)
	assert.Contains(t, out, "has runners=2 and no runner_scopes")
	out, err = runTasksLocally(t, tasks, map[string]any{"runners": 0, "github_runner_state": "present"}, nil)
	require.NoError(t, err, out)
	out, err = runTasksLocally(t, tasks, map[string]any{"runners": 0, "nova_runner_scopes": recordScopes, "github_runner_state": "absent"}, nil)
	require.Error(t, err, out)
}

// A stray's url is its .runner's gitHubUrl (the file starts with a byte-order mark); with no .runner it has none.
func TestFunctionalRecordReadsAStraysURL(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct{ file, url, path, scope string }{
		"repo":       {file: "\xef\xbb\xbf{\n  \"agentId\": 7,\n  \"agentName\": \"h-3\",\n  \"gitHubUrl\": \"https://github.com/o/r\",\n  \"workFolder\": \"_work\"\n}", url: "https://github.com/o/r", path: "o/r", scope: "repo"},
		"org":        {file: "{\"gitHubUrl\":\"https://github.com/org\"}", url: "https://github.com/org", path: "org", scope: "org"},
		"no .runner": {url: "", path: "", scope: "org"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(base, "o-r-3"), 0o755))
			if c.file != "" {
				require.NoError(t, os.WriteFile(filepath.Join(base, "o-r-3", ".runner"), []byte(c.file), 0o644))
			}
			tasks := append(taskRange(t, "roles/github-runner/tasks/record-retire.yml", "the stray runner's registration", "a removal token"),
				map[string]any{"name": "print", "ansible.builtin.debug": map[string]any{"msg": "GOT url={{ github_runner_stray_url }} path={{ github_runner_path }} scope={{ github_runner_scope }} slug={{ github_runner_slug }} index={{ github_runner_stray_index }} urls={{ github_runner_stray_urls | join(',') }}"}})
			out, err := runTasksLocally(t, tasks, map[string]any{"github_runner_base_dir": base, "github_runner_stray": "o-r-3", "github_runner_stray_urls": []any{}}, nil)
			require.NoError(t, err, out)
			assert.Contains(t, out, "GOT url="+c.url+" path="+c.path+" scope="+c.scope+" slug=o-r index=3 urls="+c.url)
			assert.Contains(t, out, "RUNNER-STRAY localhost o-r-3")
		})
	}
}

// GitHub's list of every scope the fleet's records name (this host's and another's): this host's entries the record
// does not want are deleted when offline and left when online; the other host's and the wanted ones are not touched;
// --check deletes nothing.
func TestFunctionalRecordDeletesOfflineGitHubEntriesItDoesNotWant(t *testing.T) {
	t.Parallel()
	for _, check := range []bool{false, true} {
		dir := t.TempDir()
		inv := filepath.Join(dir, "inv.yml")
		require.NoError(t, os.WriteFile(inv, []byte(`all:
  hosts:
    localhost:
      ansible_connection: local
      nova_runner_scopes: [{url: "https://github.com/o/r", count: 2, labels: []}, {url: "https://github.com/org", count: 1, labels: []}]
    other:
      nova_runner_scopes: [{url: "https://github.com/p/q", count: 1, labels: []}]
`), 0o644))
		calls := filepath.Join(dir, "gh.calls")
		gh := `echo "$*" >> ` + calls + `
[ "$2" = "-X" ] && exit 0
case "$2" in
  orgs/org/actions/runners) printf '11 localhost-1 online\n12 localhost-2 offline\n13 other-1 offline\n' ;;
  repos/o/r/actions/runners) printf '21 localhost-1 online\n22 localhost-2 online\n23 localhost-3 offline\n24 localhost-30 online\n' ;;
  repos/p/q/actions/runners) printf '31 localhost-1 offline\n32 other-1 online\n' ;;
  *) echo "unexpected: $*" >&2; exit 9 ;;
esac`
		var extra []string
		if check {
			extra = append(extra, "--check")
		}
		out, err := runTasksLocally(t, taskGroups(t, "roles/github-runner/tasks/record-github.yml")[0],
			map[string]any{"github_runner_gh": "gh", "github_runner_record_want": []any{"o-r-1", "o-r-2", "org-1"}, "github_runner_stray_urls": []any{}},
			map[string]string{"gh": gh}, append(extra, "-i", inv, "-l", "localhost")...)
		require.NoError(t, err, out)
		got, _ := os.ReadFile(calls) // ignored: compared below
		deletes := []string{}
		for _, l := range strings.Split(string(got), "\n") {
			if strings.HasPrefix(l, "api -X DELETE ") {
				deletes = append(deletes, strings.TrimPrefix(l, "api -X DELETE "))
			}
		}
		if check {
			assert.Empty(t, deletes, "--check deletes nothing")
		} else {
			assert.ElementsMatch(t, []string{"orgs/org/actions/runners/12", "repos/o/r/actions/runners/23", "repos/p/q/actions/runners/31"}, deletes)
		}
		assert.Contains(t, out, "GITHUB-STRAY localhost localhost-30 o/r online: online, so something still runs it; left")
		assert.NotContains(t, out, "other-1")
	}
}

// A runner with a job (a Runner.Worker from its bin) is waited for; one that stays busy fails the run, never stopped.
func TestFunctionalWaitIdleWaitsForTheJob(t *testing.T) {
	t.Parallel()
	tasks := taskGroups(t, "roles/github-runner/tasks/wait-idle.yml")[0]
	dir := t.TempDir()
	n := filepath.Join(dir, "n")
	// busy twice, then idle
	pgrep := `c=$(cat ` + n + ` 2>/dev/null || echo 0); c=$((c+1)); echo $c > ` + n + `
case "$*" in "-f /r/x-1/bin/Runner.Worker") ;; *) exit 3 ;; esac
[ $c -le 2 ] && { echo 4242; exit 0; }
exit 1`
	out, err := runTasksLocally(t, tasks, map[string]any{"github_runner_dir": "/r/x-1", "github_runner_idle_retries": 5, "github_runner_idle_delay": 0}, map[string]string{"pgrep": pgrep})
	require.NoError(t, err, out)
	b, _ := os.ReadFile(n) // ignored: compared below
	assert.Equal(t, "3\n", string(b), "asked until the worker was gone")

	out, err = runTasksLocally(t, tasks, map[string]any{"github_runner_dir": "/r/x-1", "github_runner_idle_retries": 2, "github_runner_idle_delay": 0}, map[string]string{"pgrep": "echo 4242"})
	require.Error(t, err, out, "a runner that stays busy fails the run")
}
