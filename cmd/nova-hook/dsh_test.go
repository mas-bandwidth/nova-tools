package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/dshdesktop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var dshArgs = []string{"hook", "--as", "ada", "--role", "comms", "--harness", "deepseek", "--dsh-events-dir", "/private/events", "--dsh-hooks-file", "/private/hooks.json", "--dsh-session", "session-1"}

func fakeDshWorld(r *rig) world {
	w := r.world()
	w.codexLock = func(string) (func() error, error) { return func() error { return nil }, nil }
	w.dshValidate = func(dshdesktop.Route) error { return nil }
	w.dshDeliver = func(dshdesktop.Route, string, string) (string, error) { return "notice", nil }
	return w
}

func TestDshRouteRefusesBeforeArming(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no harness session to discover", []string{"hook", "--as", "ada", "--role", "comms", "--harness", "deepseek", "--dsh-events-dir", "/d"}},
		{"once", append(append([]string{}, dshArgs...), "--once")},
		{"unknown harness", []string{"hook", "--as", "ada", "--role", "comms", "--harness", "muse", "--dsh-events-dir", "/d", "--dsh-hooks-file", "/d/hooks.json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			w := fakeDshWorld(r)
			var out bytes.Buffer
			code := run(tc.args, strings.NewReader(""), &out, io.Discard, w)
			assert.Equal(t, 2, code, out.String())
			assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
		})
	}
}

// recordingService captures the supervised service a hook installs without
// running it: no launchd, no process, no real time.
type recordingService struct{ args map[string][]string }

func (s *recordingService) Held(string) (string, bool) { return "", false }
func (s *recordingService) Install(as string, args []string) (string, error) {
	if s.args == nil {
		s.args = map[string][]string{}
	}
	s.args[as] = append([]string(nil), args...)
	return hookServiceName("darwin", as), nil
}
func (s *recordingService) Remove(string) error { return nil }

// dshFixture lays out a harness home under the rig's HOME the way DeepSeek
// Harness keeps it (~/.dsh/storages/workspace.json, ~/.dsh/sessions/...,
// ~/.dsh/profiles/desktop/cordis.patch.yml). It never reads the real install.
func dshFixture(t *testing.T, home string, sessions ...string) string {
	t.Helper()
	dsh := filepath.Join(home, ".dsh")
	type ws struct {
		Path       string   `json:"path"`
		Title      string   `json:"title"`
		SessionIDs []string `json:"sessionIds"`
	}
	doc := map[string]any{"tables": map[string]any{"workspaces": map[string]ws{"w1": {Path: "/Volumes/nova/ai/ada", Title: "ada", SessionIDs: sessions}}}}
	data, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dsh, "storages"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dsh, "storages", "workspace.json"), data, 0o600))
	for i, id := range sessions {
		dir := filepath.Join(dsh, "sessions", "--Volumes-nova-ai-ada--", id)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		log := filepath.Join(dir, "session.v4.jsonl.zstd")
		require.NoError(t, os.WriteFile(log, []byte("x"), 0o600))
		at := time.Date(2026, 10, 14, 12, i, 0, 0, time.UTC)
		require.NoError(t, os.Chtimes(log, at, at))
	}
	profile := filepath.Join(dsh, "profiles", "desktop")
	require.NoError(t, os.MkdirAll(profile, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(profile, "cordis.patch.yml"), []byte("# user layer\n- id: ui-theme\n  config:\n    fontSize: 22\n"), 0o600))
	return dsh
}

func TestDshOneLineHookDiscoversMountsAndInstallsTheService(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	dsh := dshFixture(t, r.home, "session-old", "session-live")
	w := fakeDshWorld(r)
	w.dshHeld = func(locks []string) map[string]bool {
		out := map[string]bool{}
		for _, l := range locks {
			out[l] = strings.Contains(l, "session-live")
		}
		return out
	}
	svc := &recordingService{}
	w.service = svc
	var out bytes.Buffer
	code := run([]string{"hook", "--as", "ada", "--role", "comms", "--harness", "dsh"}, strings.NewReader(""), &out, io.Discard, w)
	require.Equal(t, 0, code, out.String())
	assert.Contains(t, out.String(), "restart DeepSeek Harness once to load the nova-hook plugin")
	assert.Contains(t, out.String(), "session-live")

	events := filepath.Join(r.home, ".local", "state", "nova-hook", "ada-dsh")
	info, err := os.Stat(events)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(), "the default events directory is private")
	assert.Equal(t, "session-live", dshdesktop.ReadTarget(events))
	hooksFile := filepath.Join(dsh, "ada-hook", "hooks.json")
	_, err = os.Stat(hooksFile)
	require.NoError(t, err)
	assert.True(t, dshdesktop.Mounted(dshdesktop.ProfileDir(dsh), hooksFile), "the include is mounted in the desktop profile")

	args := strings.Join(svc.args["ada"], " ")
	assert.Contains(t, args, "--harness dsh")
	assert.NotContains(t, args, "--dsh-session", "the service rediscovers and follows; it never pins the session it was installed on")
	assert.NotContains(t, args, "--dsh-events-dir")
}

func TestDshOneLineRefusesWithoutAWorkspaceAndHonoursOverrides(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	dshFixture(t, r.home) // no session at all
	w := fakeDshWorld(r)
	w.service = &recordingService{}
	var out, errOut bytes.Buffer
	code := run([]string{"hook", "--as", "ada", "--role", "comms", "--harness", "dsh"}, strings.NewReader(""), &out, &errOut, w)
	assert.Equal(t, 2, code, out.String())
	assert.Contains(t, out.String()+errOut.String(), "--dsh-session")
	assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)

	// A pinned session needs no workspace; the pinned id reaches the service.
	svc := &recordingService{}
	w.service = svc
	out.Reset()
	code = run([]string{"hook", "--as", "ada", "--role", "comms", "--harness", "dsh", "--dsh-session", "session-pinned"}, strings.NewReader(""), &out, io.Discard, w)
	require.Equal(t, 0, code, out.String())
	assert.Contains(t, strings.Join(svc.args["ada"], " "), "--dsh-session session-pinned")
}
