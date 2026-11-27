package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// github_runner_stale_test.go pins two steps the runner login's move once took by hand, now the play's: a snap whose
// service leaves a socket every login may write (hwctl) is stopped and disabled before the lockdown scan, and a
// hand-made account of the runner login's name with a uid below 1000 is retired only on request, only when nothing
// uses it, archived first and never deleted with its home.

func TestRunnerLockdownTurnsOffHwctlBeforeTheScan(t *testing.T) {
	t.Parallel()
	d := runnerDefaults(t)
	snaps, _ := d["github_runner_snaps_off"].([]any)
	require.Len(t, snaps, 1)
	hw, _ := snaps[0].(map[string]any)
	assert.Equal(t, "hwctl", hw["snap"])
	assert.Equal(t, "/var/snap/hwctl/common/hwctl.varlink", hw["socket"])

	tasks := roleTaskFile(t, "lockdown.yml")
	stop := runnerTask(t, tasks, "each such snap's services are stopped and disabled")
	cmd, _ := stop["ansible.builtin.command"].(map[string]any)
	assert.Equal(t, []any{"snap", "stop", "--disable", "{{ item.item.snap }}"}, cmd["argv"])
	assert.Equal(t, "(item.stdout | trim) == 'ON'", asText(stop["when"]), "a machine without the snap, or with it off, is left as it is")
	sock := asText(runnerTask(t, tasks, "each such snap's socket file goes once nothing listens on it")["ansible.builtin.shell"])
	assert.Contains(t, sock, "ss -xlnH", "a socket something listens on is never removed")
	assert.Contains(t, sock, `[ -S "$s" ]`, "only a socket file is removed")
	for _, name := range []string{"the services of each snap", "each such snap's services are stopped", "each such snap's socket file goes"} {
		assert.Less(t, taskIndex(tasks, name), taskIndex(tasks, "the runner login can write no system path"), name)
	}
}

func TestRunnerStaleLoginIsRetiredOnlyOnRequestAndWhenUnused(t *testing.T) {
	t.Parallel()
	d := runnerDefaults(t)
	assert.Equal(t, false, d["github_runner_retire_stale_login"], "off unless a run asks for it")

	login := roleTaskFile(t, "unix-login.yml")
	which := asText(runnerTask(t, login, "whether a stale account of the name is to be retired")["ansible.builtin.set_fact"].(map[string]any)["github_runner_stale"])
	assert.Contains(t, which, "github_runner_retire_stale_login | bool")
	assert.Contains(t, which, "github_runner_existing.stdout_lines[0] | int < 1000")
	assert.Less(t, taskIndex(login, "a stale account of the name is retired"), taskIndex(login, "an existing account is a runner login"))
	assert.Less(t, taskIndex(login, "the name is free once the stale account is retired"), taskIndex(login, "the runner login (locked"))

	stale := roleTaskFile(t, "stale-login.yml")
	use := asText(runnerTask(t, stale, "what still uses the stale account")["ansible.builtin.shell"])
	for _, want := range []string{"pgrep -u", "User=", "/etc/sudoers.d", "/var/spool/cron/crontabs", "authorized_keys", "lastlog", "last -w", "findmnt --real", "-uid"} {
		assert.Contains(t, use, want)
	}
	retire := runnerTask(t, stale, "the stale account is archived, deleted and its home moved under /var/backups")
	assert.Equal(t, "not ansible_check_mode", asText(retire["when"]))
	sh := asText(retire["ansible.builtin.shell"])
	assert.Contains(t, sh, "userdel \"$u\"\n", "never userdel -r: the home is kept")
	assert.NotContains(t, sh, "userdel -r")
	assert.NotContains(t, sh, "rm ")
	require.Contains(t, sh, "tar -C")
	require.Contains(t, sh, `[ "$want" = "$got" ] || {`, "every entry of the archive is counted against the home")
	assert.Less(t, strings.Index(sh, "tar -C"), strings.Index(sh, "userdel"), "archived before the account goes")
	assert.Less(t, strings.Index(sh, `[ "$want" = "$got" ]`), strings.Index(sh, "userdel"), "every entry checked before the account goes")
	assert.Less(t, taskIndex(stale, "a stale account still in use is not retired"), taskIndex(stale, "the stale account is archived"))
}
