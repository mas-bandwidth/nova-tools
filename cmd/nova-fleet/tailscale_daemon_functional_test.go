//go:build functional

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Tailscale SSH is off by default and the role says so to the daemon: a join passes --ssh=false and a running node that
// serves it is set to --ssh=false, so a run turns it off where an earlier release turned it on. Tailscale SSH trusts
// the machine, not the person: with it on, every worker on a tagged machine is let in.
func TestTailscaleRoleTurnsTailscaleSSHOff(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	raw, err := os.ReadFile("roles/tailscale/defaults/main.yml")
	require.NoError(t, err)
	defaults := map[string]any{}
	require.NoError(t, yaml.Unmarshal(raw, &defaults))
	assert.Equal(t, false, defaults["nova_tailscale_ssh"], "Tailscale SSH defaults to off")

	file := "roles/tailscale/tasks/main.yml"
	join := roleTask(t, file, "what the join must do")["ansible.builtin.set_fact"]
	set := roleTask(t, file, "tailscale set (hostname, ssh)")
	require.NotNil(t, join)
	require.NotNil(t, set)
	argv := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(asText(set["ansible.builtin.command"].(map[string]any)["argv"])), "{{"), "}}")
	play := []map[string]any{{
		"hosts": "all", "connection": "local", "gather_facts": false,
		"tasks": []map[string]any{
			{"ansible.builtin.set_fact": join},
			{"ansible.builtin.debug": map[string]any{"msg": "FLAG={{ ts_ssh_flag | join(',') }} SET={{ (" + argv + ") | join(',') }} WHEN={{ " + strings.ReplaceAll(asText(set["when"]), "\n", " ") + " }}"}},
		},
	}}
	b, err := yaml.Marshal(play)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "render.yml")
	require.NoError(t, os.WriteFile(path, b, 0o644))
	for _, tc := range []struct {
		name string
		vars map[string]any
		want string
	}{
		{"a join on Linux", map[string]any{"ts_root_daemon": true, "ts_state": "Absent"}, "FLAG=--ssh=false "},
		{"a running node that serves it", map[string]any{"ts_root_daemon": true, "ts_state": "Running", "ts_prefs": map[string]any{"stdout": `{"RunSSH": true}`}},
			"SET=tailscale,set,--hostname=space,--ssh=false WHEN=True"},
		{"a running node that does not", map[string]any{"ts_root_daemon": true, "ts_state": "Running", "ts_prefs": map[string]any{"stdout": `{"RunSSH": false}`}}, "WHEN=False"},
		{"the macOS app (it cannot serve it)", map[string]any{"ts_root_daemon": false, "ts_state": "Absent"}, "FLAG= "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ev := map[string]any{"nova_tailscale_ssh": defaults["nova_tailscale_ssh"], "ts_bin": "tailscale",
				"ts_self": map[string]any{"HostName": "space", "Tags": []string{"tag:nova"}}, "ts_tags": []string{"tag:nova"}}
			for k, v := range tc.vars {
				ev[k] = v
			}
			j, err := json.Marshal(ev)
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "ansible-playbook", "-i", "space,", "-c", "local", "-e", string(j), path)
			cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1")
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
			assert.Contains(t, string(out), tc.want, string(out))
		})
	}
}

// The tailscale role drives a Mac running the open-source tailscaled (Homebrew's formula, as root) as it drives Linux:
// the CLI beside the daemon, sudo, Tailscale SSH served, and no app installed over it. The role's own set_fact is
// rendered through ansible-core's templar for each platform and daemon.
func TestTailscaleRoleDrivesTheOpenSourceDaemonOnAMac(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine")
	}
	file := "roles/tailscale/tasks/main.yml"
	facts := roleTask(t, file, "the daemon this play drives")["ansible.builtin.set_fact"]
	require.NotNil(t, facts)
	assert.Contains(t, asText(roleTask(t, file, "install (macOS")["when"]), "not ts_root_daemon", "the app is never installed over the open-source daemon")
	assert.Contains(t, asText(roleTask(t, file, "tailscale set (hostname, ssh)")["become"]), "ts_root_daemon")

	play := []map[string]any{{
		"hosts": "localhost", "connection": "local", "gather_facts": false,
		"tasks": []map[string]any{
			{"ansible.builtin.set_fact": facts},
			{"ansible.builtin.debug": map[string]any{"msg": "ROOT={{ ts_root_daemon }} BIN={{ ts_bin }}"}},
		},
	}}
	b, err := yaml.Marshal(play)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "render.yml")
	require.NoError(t, os.WriteFile(path, b, 0o644))

	app := "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
	for _, tc := range []struct {
		name   string
		os     string
		daemon map[string]any
		want   string
	}{
		{"linux", "debian", map[string]any{"skipped": true}, "ROOT=True BIN=" + app},
		{"mac with the formula", "darwin", map[string]any{"stdout": "/opt/homebrew/opt/tailscale/bin/tailscaled\n"}, "ROOT=True BIN=/opt/homebrew/opt/tailscale/bin/tailscale"},
		{"mac with the app", "darwin", map[string]any{"stdout": ""}, "ROOT=False BIN=" + app},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ev, err := json.Marshal(map[string]any{"ts_os": tc.os, "ts_daemon": tc.daemon, "nova_tailscale_bin": app})
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "ansible-playbook", "-i", "localhost,", "-e", string(ev), path)
			cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1")
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
			assert.Contains(t, string(out), tc.want, string(out))
		})
	}
}
