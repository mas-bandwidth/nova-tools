//go:build functional

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

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
