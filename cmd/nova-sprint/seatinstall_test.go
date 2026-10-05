package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// seat install and seat uninstall through the command: the unit goes into the home
// the test gives, runs this binary's inbox --wait --push seat on the store the verb
// was given, and is loaded by the test's loader, never launchctl or systemctl.
func TestSeatInstallVerbWritesLoadsAndRemovesTheUnit(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	env := map[string]string{"NOVA_SPRINT_REDIS": "127.0.0.1:6381"}
	a := newApp(func(k string) string { return env[k] })
	a.goos = "darwin"
	a.home = func() (string, error) { return home, nil }
	a.executable = func() (string, error) { return "/opt/nova/bin/nova-sprint", nil }
	var calls []string
	a.seatLoad = func(goos, op, path string) error { calls = append(calls, goos+" "+op+" "+path); return nil }
	do := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := a.run(args, &out, &errb)
		return code, out.String(), errb.String()
	}
	unit := filepath.Join(home, "Library", "LaunchAgents", sprint.SeatLabel+".plist")

	code, out, errs := do("seat", "install", "--dry-run")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "SEAT INSTALL DRY-RUN unit="+unit)
	assert.Contains(t, out, "<string>--push</string>\n\t\t<string>seat</string>\n\t\t<string>--redis</string>\n\t\t<string>127.0.0.1:6381</string>")
	assert.Contains(t, out, filepath.Join(home, "Library", "Logs", "nova-sprint-seat-push.log"))
	assert.NoFileExists(t, unit, "a dry run writes nothing")
	assert.Empty(t, calls, "a dry run loads nothing")

	code, out, errs = do("seat", "install")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "SEAT INSTALL OK unit="+unit+" written=true loaded=true")
	assert.Contains(t, out, "runs: /opt/nova/bin/nova-sprint inbox --wait --push seat --redis 127.0.0.1:6381")
	assert.FileExists(t, unit)
	assert.Equal(t, []string{"darwin load " + unit}, calls)

	code, out, errs = do("seat", "uninstall", "--dry-run")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "SEAT UNINSTALL DRY-RUN unit="+unit+" present=true")
	assert.FileExists(t, unit, "a dry run removes nothing")
	assert.Equal(t, []string{"darwin load " + unit}, calls, "a dry run unloads nothing")

	code, out, errs = do("seat", "uninstall", "--json")
	require.Equal(t, 0, code, errs)
	var got struct {
		Path    string `json:"path"`
		Removed bool   `json:"removed"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	assert.Equal(t, unit, got.Path)
	assert.True(t, got.Removed)
	assert.NoFileExists(t, unit)
	assert.Equal(t, []string{"darwin load " + unit, "darwin unload " + unit}, calls)

	// the sprint's server, when the verb is given no --redis of its own
	env["NOVA_SPRINT_SERVER"] = "127.0.0.1:7480"
	dir := t.TempDir()
	a.goos = "linux"
	code, out, errs = do("seat", "install", "--dir", dir)
	require.Equal(t, 0, code, errs)
	b, err := os.ReadFile(filepath.Join(dir, sprint.SeatService))
	require.NoError(t, err)
	assert.Contains(t, string(b), `Environment="NOVA_SPRINT_SERVER=127.0.0.1:7480"`)
	assert.NotContains(t, string(b), "--redis", out)

	// the twin has no machine to wait on: refused, nothing written or loaded
	delete(env, "NOVA_SPRINT_SERVER")
	env["NOVA_SPRINT_REDIS"] = "mem:0"
	before := len(calls)
	code, _, errs = do("seat", "install", "--dir", t.TempDir())
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "nova-sprint seat install REFUSED: ")
	assert.Equal(t, before, len(calls))
}
