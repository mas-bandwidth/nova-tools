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

	"github.com/mas-bandwidth/nova-tools/internal/member"
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
