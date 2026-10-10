package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
)

// seat install and seat uninstall through the command: the unit goes into the home
// the test gives, runs this binary's inbox --wait --push seat on the store the verb
// was given, and is loaded by the test's loader, never launchctl or systemctl.
func TestSeatInstallVerbWritesLoadsAndRemovesTheUnit(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	env := map[string]string{"NOVA_SPRINT_REDIS": "127.0.0.1:6381", "NOVA_SPRINT_ACTOR": "rowan"}
	a := newApp(func(k string) string { return env[k] })
	m := store.NewMem()
	a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return m, nil }
	var asked [][]string
	a.forward = func(_ context.Context, _ string, verbs ...[]string) ([]sprintwire.Result, error) {
		asked = append(asked, verbs...)
		return []sprintwire.Result{{}}, nil
	}
	session := t.TempDir()
	push := []string{"--harness", "opencode", "--target", session}
	a.goos = "darwin"
	a.home = func() (string, error) { return home, nil }
	a.executable = func() (string, error) { return "/opt/nova/bin/nova-sprint", nil }
	var calls []string
	a.seatLoad = func(goos, op, path string) error { calls = append(calls, goos+" "+op+" "+path); return nil }
	do := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		if len(args) > 1 && args[1] == "install" {
			args = append(args, push...)
		}
		code := a.run(args, &out, &errb)
		return code, out.String(), errb.String()
	}
	unit := filepath.Join(home, "Library", "LaunchAgents", sprint.SeatLabel+".plist")

	// no recorded login and no shell user: refused, nothing written
	refuseDir := t.TempDir()
	loginPath := filepath.Join(home, ".config", "nova-sprint", "login.json")
	code, out, errs := do("seat", "install", "--dir", refuseDir)
	require.Equal(t, 2, code, errs)
	assert.Empty(t, out)
	assert.Contains(t, errs, "no seat login is recorded at "+loginPath+"; run: nova-sprint seat login --store <dir> --as <seat> --key <file> --secret <NAME> --user <name> --redis <addr>")
	assert.NoFileExists(t, filepath.Join(refuseDir, sprint.SeatUnitFile(a.seatOS())))
	assert.Empty(t, calls)

	// a shell user with no recorded login is unitLogin's sentence, and still writes nothing
	env["NOVA_SPRINT_REDIS_USER"] = "coordinator"
	code, out, errs = do("seat", "install", "--dir", refuseDir)
	require.Equal(t, 2, code, errs)
	assert.Empty(t, out)
	assert.Contains(t, errs, "the unit carries no password, and the store 127.0.0.1:6381 is logged in to here as coordinator from this shell's environment: record the login the unit reads in its own process, run: nova-sprint seat login --redis 127.0.0.1:6381 --user coordinator --store <dir> --as <seat> --key <file> --secret <NAME>")
	assert.NoFileExists(t, filepath.Join(refuseDir, sprint.SeatUnitFile(a.seatOS())))
	delete(env, "NOVA_SPRINT_REDIS_USER")
	recordSeatLogin(t, a, "127.0.0.1:6381")

	code, out, errs = do("seat", "install", "--dry-run")
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
	body, err := os.ReadFile(unit)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "NOVA_SPRINT_REDIS_USER")
	assert.NotContains(t, string(body), "PW")
	assert.Equal(t, []string{"darwin load " + unit}, calls)
	// the push target is recorded with it: the push loop reaches the session through it
	assert.Contains(t, out, "push: opencode into "+session)
	st := &store.Store{B: m, Names: sprint.Names{}, Now: time.Now}
	rec, ok, err := readPush(context.Background(), st, "rowan")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, sprint.PushRecord{Name: "rowan", Harness: "opencode", Target: session}, rec)

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

	// the sprint's server, when the verb is given no --redis of its own: a server
	// unit does not dial Redis, so it installs with no recorded login
	a.loginFile = nil
	env["NOVA_SPRINT_SERVER"] = "127.0.0.1:7480"
	dir := t.TempDir()
	a.goos = "linux"
	code, out, errs = do("seat", "install", "--dir", dir)
	require.Equal(t, 0, code, errs)
	b, err := os.ReadFile(filepath.Join(dir, sprint.SeatService))
	require.NoError(t, err)
	assert.Contains(t, string(b), `Environment="NOVA_SPRINT_SERVER=127.0.0.1:7480"`)
	assert.NotContains(t, string(b), "--redis", out)
	assert.NotContains(t, string(b), "NOVA_SPRINT_REDIS_USER")
	require.Len(t, asked, 1, "the push target goes to the server")
	assert.Equal(t, []string{"seat", "--actor", "rowan", "push", "--actor", "rowan", "--harness", "opencode", "--target", session}, asked[0])

	// the twin has no machine to wait on: refused, nothing written or loaded
	delete(env, "NOVA_SPRINT_SERVER")
	env["NOVA_SPRINT_REDIS"] = "mem:0"
	before := len(calls)
	code, _, errs = do("seat", "install", "--dir", t.TempDir())
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "nova-sprint seat install REFUSED: ")
	assert.Equal(t, before, len(calls))
}

// recordSeatLogin records a login for addr. A Redis seat unit is installed only
// when that login matches. The file names the secret and holds no value.
func recordSeatLogin(t *testing.T, a *app, addr string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "login.json")
	b, err := json.Marshal(storeLogin{Redis: addr, User: "coordinator", Store: dir, As: "studio", Key: filepath.Join(dir, "k"), Sops: "/usr/bin/sops", Secret: "PW"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(b, '\n'), 0o600))
	a.loginFile = func() (string, error) { return path, nil }
}
