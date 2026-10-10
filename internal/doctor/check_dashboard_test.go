package doctor

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dashboardEnv confines even the system unit directories to this test's
// temporary root. The generic fakeEnv intentionally reads absolute paths on
// the host, which would pick up a runner's real dashboard service here.
type dashboardEnv struct{ fakeEnv }

func (d dashboardEnv) fixturePath(p string) string {
	return filepath.Join(d.root, strings.TrimPrefix(filepath.Clean(p), string(filepath.Separator)))
}

func (d dashboardEnv) ReadFile(p string) ([]byte, error) { return os.ReadFile(d.fixturePath(p)) }
func (d dashboardEnv) ReadDir(p string) ([]fs.DirEntry, error) {
	return os.ReadDir(d.fixturePath(p))
}

const (
	dashboardPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!-- /Users/ada/Library/LaunchAgents/com.nova.loop.sprint-dashboard.plist: written by fleet/loops.yml from the loop record sprint-dashboard -->
<plist version="1.0"><dict><key>ProgramArguments</key><array>
<string>env</string><string>NOVA_SPRINT_SERVER=127.0.0.1:6390</string><string>nova-sprint</string><string>dashboard</string>
<string>--listen</string><string>100.64.0.9:7390,127.0.0.1:7391</string></array></dict></plist>
`
	dashboardService = `# /home/ada/.config/systemd/user/nova-loop-dash.service: written by fleet/loops.yml from the loop record dash
[Service]
ExecStart=/usr/bin/env NOVA_SPRINT_SERVER=127.0.0.1:6390 nova-sprint dashboard --logo /home/ada/logo.webp
`
	memberPlist = `<!-- written by fleet/loops.yml from the loop record m1 -->
<array><string>nova-swarm</string><string>member</string></array>`
)

// dashboardRig is a home under t.TempDir() holding the named unit files (path under the
// home: text), and a dial that answers on the addresses in up.
func dashboardRig(t *testing.T, units map[string]string, up ...string) Env {
	t.Helper()
	root := t.TempDir()
	for p, text := range units {
		full := filepath.Join(root, "home", p)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(text), 0o644))
	}
	return dashboardEnv{fakeEnv{env: map[string]string{"HOME": "home"}, root: root, rootAbsolute: true,
		dial: func(addr string) error {
			for _, a := range up {
				if a == addr {
					return nil
				}
			}
			return errors.New("connection refused")
		}}}
}

func TestDashboardCheck(t *testing.T) {
	t.Parallel()
	run := func(env Env) Result {
		r := NewRegistry()
		r.Register(Default.checks["dashboard"])
		res, skipped, err := r.Run(context.Background(), env, Options{})
		require.NoError(t, err)
		require.Empty(t, skipped)
		require.Len(t, res, 1)
		return res[0]
	}
	launchd := "Library/LaunchAgents/com.nova.loop.sprint-dashboard.plist"
	systemd := ".config/systemd/user/nova-loop-dash.service"

	t.Run("the loop record's unit is there and its loopback port answers", func(t *testing.T) {
		t.Parallel()
		r := run(dashboardRig(t, map[string]string{launchd: dashboardPlist, "Library/LaunchAgents/com.nova.loop.m1.plist": memberPlist}, "127.0.0.1:7391"))
		assert.Equal(t, OK, r.Status, r)
		assert.Equal(t, "home/"+launchd+" runs nova-sprint dashboard and 127.0.0.1:7391 answers", r.Evidence)
	})
	t.Run("a systemd unit with no --listen is dialled on the default", func(t *testing.T) {
		t.Parallel()
		r := run(dashboardRig(t, map[string]string{systemd: dashboardService}, "127.0.0.1:7390"))
		assert.Equal(t, OK, r.Status, r)
	})
	t.Run("the port does not answer: a fail naming the loop", func(t *testing.T) {
		t.Parallel()
		r := run(dashboardRig(t, map[string]string{systemd: dashboardService}))
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "127.0.0.1:7390 does not answer: connection refused")
		assert.Contains(t, r.Fix, "nova-config loop show dash")
	})
	t.Run("no loop record runs the dashboard: a warn with the loop add line", func(t *testing.T) {
		t.Parallel()
		r := run(dashboardRig(t, map[string]string{"Library/LaunchAgents/com.nova.loop.m1.plist": memberPlist}))
		assert.Equal(t, Warn, r.Status, r)
		assert.Equal(t, "no loop record's unit runs nova-sprint dashboard on this machine", r.Evidence)
		assert.Contains(t, r.Fix, "nova-config loop add sprint-dashboard")
	})
	t.Run("a hand plist is named, alone or beside the loop record", func(t *testing.T) {
		t.Parallel()
		hand := "Library/LaunchAgents/com.nova.dashboard.public.plist"
		r := run(dashboardRig(t, map[string]string{hand: "<plist><string>python3</string><string>server.py</string></plist>"}))
		assert.Equal(t, Warn, r.Status, r)
		assert.Contains(t, r.Evidence, "home/"+hand+" is a hand unit serving the dashboard")
		r = run(dashboardRig(t, map[string]string{hand: "<plist/>", launchd: dashboardPlist}, "127.0.0.1:7391"))
		assert.Equal(t, Warn, r.Status, r)
		assert.Equal(t, "unload and remove home/"+hand+": the dashboard runs as its loop record alone", r.Fix)
	})
	t.Run("a unit whose --listen names no loopback address is a fail with the listen fix", func(t *testing.T) {
		t.Parallel()
		unit := `<!-- written by fleet/loops.yml from the loop record d --><string>nova-sprint</string><string>dashboard</string><string>--listen</string><string>none</string>`
		r := run(dashboardRig(t, map[string]string{launchd: unit}))
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "home/"+launchd+" runs nova-sprint dashboard but its --listen names no loopback address")
		assert.Contains(t, r.Fix, "nova-config loop show sprint-dashboard")
	})
	t.Run("only a fleet needs it", func(t *testing.T) {
		t.Parallel()
		assert.True(t, Default.checks["dashboard"].Fleet)
	})
}
