package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// TestMemberUnitsStopByDraining: a member loop's unit stops it by draining
// (nova-tools#5096 item 26). Its supervisor signals the member alone (systemd's
// KillMode=mixed: SIGTERM to the main process, never the whole cgroup; launchd
// signals the job's process and abandons its group) and waits the stop timeout,
// nova_member_stop_timeout, before it kills what is left: a minute above
// member.DrainMost, the longest a member's drain lasts, so a member always stops by
// itself first.
func TestMemberUnitsStopByDraining(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	var vars map[string]any
	b, err := os.ReadFile(filepath.Join(root, "fleet", "group_vars", "all.yml"))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(b, &vars))
	assert.Equal(t, int((member.DrainMost + time.Minute).Seconds()), vars["nova_member_stop_timeout"])
	// A member that drains does no new work, so its stop is bounded well under an
	// hour: the timeout stays at most 300 s, and a restart never holds a machine
	// for a drain of hours.
	assert.LessOrEqual(t, vars["nova_member_stop_timeout"], 300)

	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(root, "fleet", "templates", name))
		require.NoError(t, err)
		return string(b)
	}
	service := read("nova-loop.service.j2")
	assert.Contains(t, service, "{% if loop_member %}\nKillMode=mixed\nTimeoutStopSec={{ nova_member_stop_timeout }}\n{% endif %}")
	plist := read("nova-loop.plist.j2")
	assert.Contains(t, plist, "{% if loop_member %}\n<key>ExitTimeOut</key>\n<integer>{{ nova_member_stop_timeout }}</integer>\n{% endif %}")
	loops, err := os.ReadFile(filepath.Join(root, "fleet", "loops.yml"))
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(loops), "loop_member: "), "the play says which records are members")
}

// TestAWaitForANonZeroExitIsNotAFailure: a task that retries a command until it
// exits non-zero (the drain wait: launchctl print fails once launchd no longer
// holds the member) must not fail on that exit. A command task fails on any
// non-zero rc unless failed_when says otherwise, so without it the exit the task
// waits for failed the host, and the play never loaded the new units: the
// Studio's sprint server and member stayed unloaded, 2026-10-02 7:36 PM.
func TestAWaitForANonZeroExitIsNotAFailure(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	plays, err := filepath.Glob(filepath.Join(root, "fleet", "*.yml"))
	require.NoError(t, err)
	waits := 0
	for _, p := range plays {
		b, err := os.ReadFile(p)
		require.NoError(t, err)
		var doc []struct {
			Tasks []map[string]any `yaml:"tasks"`
		}
		if yaml.Unmarshal(b, &doc) != nil {
			continue // a vars file, not a play
		}
		for _, play := range doc {
			for _, task := range play.Tasks {
				until, _ := task["until"].(string)
				if !strings.Contains(until, ".rc != 0") {
					continue
				}
				waits++
				_, ok := task["failed_when"]
				assert.True(t, ok, "%s: task %q waits for a non-zero exit (until: %s) and has no failed_when, so that exit fails the host", filepath.Base(p), task["name"], until)
			}
		}
	}
	assert.NotZero(t, waits, "the drain wait in fleet/loops.yml is found")
}
