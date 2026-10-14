package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/harness"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeNoticeFoldsABurstIntoOneDelivery(t *testing.T) {
	t.Parallel()
	idle := newClaudeNotice()
	idle.offer(50, "")
	d, ok := idle.take()
	require.True(t, ok)
	assert.True(t, d.Wake)
	assert.Equal(t, "49 more messages; read the bus", d.Text)
	_, ok = idle.take()
	assert.False(t, ok)

	folded := newClaudeNotice()
	for i := 0; i < 50; i++ {
		folded.offer(1, "")
	}
	d, ok = folded.take()
	require.True(t, ok)
	assert.Equal(t, 50, d.Count)
	assert.Equal(t, "49 more messages; read the bus", d.Text)
	_, ok = folded.take()
	assert.False(t, ok)

	busy := newClaudeNotice()
	busy.askIdle = func() bool { return false }
	busy.offer(50, "")
	d, ok = busy.take()
	require.True(t, ok)
	assert.False(t, d.Wake)
	assert.Contains(t, d.Text, "49 more messages")
}

func TestClaudeNoticeWakesOnceAfterTheBoundary(t *testing.T) {
	t.Parallel()
	a := newClaudeNotice()
	a.askIdle = func() bool { return false }
	a.offer(1, "")
	d, ok := a.take()
	require.True(t, ok)
	assert.False(t, d.Wake)
	assert.Equal(t, "bus message; read the bus", d.Text)

	a.offer(40, "")
	_, ok = a.take()
	assert.False(t, ok, "a burst while the turn is held stays folded")

	a.askIdle = nil
	d, ok = a.boundary()
	require.True(t, ok)
	assert.True(t, d.Wake)
	assert.Equal(t, "39 more messages; read the bus", d.Text)
	_, ok = a.take()
	assert.False(t, ok)
}

func TestClaudeNoticeKeepsTheNewestProveLine(t *testing.T) {
	t.Parallel()
	a := newClaudeNotice()
	a.offer(1, "run: nova-hook prove --as ada --nonce old")
	a.offer(2, "run: nova-hook prove --as ada --nonce new")
	d, ok := a.take()
	require.True(t, ok)
	assert.Equal(t, "run: nova-hook prove --as ada --nonce new; 2 more messages; read the bus", d.Text)
	assert.NotContains(t, d.Text, "old")

	one := newClaudeNotice()
	one.offer(1, "run: nova-hook prove --as ada --nonce only")
	d, ok = one.take()
	require.True(t, ok)
	assert.Equal(t, "run: nova-hook prove --as ada --nonce only", d.Text)
}

func TestClaudeInstallIsIdempotentAndKeepsExistingSettings(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	settings := t.TempDir()
	path := filepath.Join(settings, claudeSettingsFile)
	require.NoError(t, os.WriteFile(path, []byte("{\n  \"theme\": \"dark\",\n  \"hooks\": {\n    \"Stop\": [\n      {\"hooks\": [{\"type\": \"command\", \"command\": \"echo keep\"}]}\n    ]\n  }\n}\n"), 0o600))
	setup := claudeSetup{as: "ada", role: "comms", state: state, settings: settings}
	require.NoError(t, installClaudeFiles(setup))
	require.NoError(t, installClaudeFiles(setup))

	script := filepath.Join(settings, claudeBoundaryScript)
	info, err := os.Stat(script)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	body, err := os.ReadFile(script)
	require.NoError(t, err)
	text := string(body)
	assert.Contains(t, text, "--as 'ada'")
	assert.Contains(t, text, "--role 'comms'")
	assert.Contains(t, text, "--harness '"+harness.Claude+"'")
	assert.Contains(t, text, "--claude-boundary")

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var root map[string]any
	require.NoError(t, json.Unmarshal(raw, &root))
	assert.Equal(t, "dark", root["theme"])
	assert.Equal(t, 1, countStopCommand(t, root, script))
	assert.Contains(t, string(raw), "echo keep")

	before := append([]byte(nil), raw...)
	require.NoError(t, installClaudeFiles(setup))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)

	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	err = installClaudeFiles(setup)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JSON object")
}

func countStopCommand(t *testing.T, root map[string]any, command string) int {
	t.Helper()
	hooks := root["hooks"].(map[string]any)
	groups := hooks["Stop"].([]any)
	n := 0
	for _, g := range groups {
		for _, h := range g.(map[string]any)["hooks"].([]any) {
			if h.(map[string]any)["command"] == command {
				n++
			}
		}
	}
	return n
}

func TestClaudeHookRefusesBeforeArming(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	settings := t.TempDir()
	base := []string{"hook", "--as", "ada", "--role", "comms", "--harness", harness.Claude, "--claude-state", state, "--claude-settings", settings}
	for _, tc := range []struct {
		name    string
		args    []string
		install error
		want    string
	}{
		{name: "install", args: base, install: errors.New("settings refused"), want: "settings refused"},
		{name: "unknown", args: []string{"hook", "--as", "ada", "--role", "comms", "--harness", "claude-code"}, want: "hook harness wants " + harness.Claude},
		{name: "capital", args: []string{"hook", "--as", "ada", "--role", "comms", "--harness", "Claude"}, want: "hook harness wants " + harness.Claude},
		{name: "once", args: append(append([]string{}, base...), "--once"), want: "omit --once"},
		{name: "codex", args: append(append([]string{}, base...), "--codex-socket", "/private/socket", "--codex-thread", "thread-1", "--codex-events-dir", "/private/events"), want: "do not run in one hook"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			w := r.world()
			opened := false
			prev := w.open
			w.open = func(ctx context.Context, addr string) (bus.Store, string, func(), error) {
				opened = true
				return prev(ctx, addr)
			}
			installed := false
			w.claudeInstall = func(claudeSetup) error {
				installed = true
				return tc.install
			}
			var out bytes.Buffer
			code := run(tc.args, strings.NewReader(""), &out, &out, w)
			assert.Equal(t, 2, code, out.String())
			assert.Contains(t, out.String(), tc.want)
			assert.False(t, opened)
			assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
			if tc.name != "install" {
				assert.False(t, installed)
			}
		})
	}
}

func TestClaudeDryRunDoesNotInstall(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.world()
	state := t.TempDir()
	settings := t.TempDir()
	installed := false
	w.claudeInstall = func(claudeSetup) error {
		installed = true
		return nil
	}
	var out bytes.Buffer
	args := []string{"hook", "--as", "ada", "--role", "comms", "--harness", harness.Claude, "--claude-state", state, "--claude-settings", settings, "--dry-run"}
	code := run(args, strings.NewReader(""), &out, &out, w)
	assert.Zero(t, code, out.String())
	assert.False(t, installed)
	assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
	_, err := os.Stat(filepath.Join(settings, claudeSettingsFile))
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.Contains(t, out.String(), "DRY-RUN")
}

func TestClaudeBoundaryAdvancesGenerationWithoutArming(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.world()
	state := t.TempDir()
	settings := t.TempDir()
	opened := false
	w.open = func(context.Context, string) (bus.Store, string, func(), error) {
		opened = true
		return nil, "", nil, errors.New("open")
	}
	w.claudeInstall = func(claudeSetup) error {
		t.Fatal("boundary installs nothing")
		return nil
	}
	args := []string{"hook", "--as", "ada", "--role", "comms", "--harness", harness.Claude, "--claude-state", state, "--claude-settings", settings, "--claude-boundary"}
	var out bytes.Buffer
	code := run(args, strings.NewReader(""), &out, &out, w)
	assert.Zero(t, code, out.String())
	assert.Contains(t, out.String(), "generation=1")
	assert.False(t, opened)
	assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)

	out.Reset()
	code = run(args, strings.NewReader(""), &out, &out, w)
	assert.Zero(t, code, out.String())
	assert.Contains(t, out.String(), "generation=2")

	a := newClaudeNotice()
	a.genPath = filepath.Join(state, claudeGenerationName)
	a.seen = 0
	a.delivered = true
	a.idle = false
	a.offer(40, "")
	w.claude = a
	var buf bytes.Buffer
	require.Nil(t, w.pollClaude(&tool.Call{Stdout: &buf}))
	assert.Contains(t, buf.String(), "39 more messages; read the bus")
	assert.Contains(t, buf.String(), "wake=true")
	assert.NotContains(t, buf.String(), "secret")
}

func TestClaudeLoopDeliversOneNoticeForABurst(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.world()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
	epoch, _, err := r.h.Store.HookAs(ctx, "ada", hook.Comms, "test")
	require.NoError(t, err)
	var entries []bus.Entry
	for i := 0; i < 50; i++ {
		m := bus.Message{ID: fmt.Sprintf("m%d", i), From: "bob", Subject: "secret-subject", Body: "secret-body"}
		entries = append(entries, bus.Entry{Entry: fmt.Sprintf("1-%d", i), Fields: m.Fields()})
	}
	entries = append(entries, bus.Entry{Entry: "1-50", Fields: bus.Message{ID: "own", From: "ada", Subject: "secret-subject", Body: "own-body"}.Fields()})
	calls := 0
	st := claudeWaiter{Store: bus.Redis{C: r.h.Client}, read: func() []bus.Entry {
		calls++
		if calls > 1 {
			cancel()
			return nil
		}
		return entries
	}}
	s := &session{bus: st, hooks: r.h.Store, as: "ada", addr: "127.0.0.1:6379"}
	var got []claudeDelivery
	w.claude = newClaudeNotice()
	w.claude.deliver = func(d claudeDelivery) error {
		got = append(got, d)
		return nil
	}
	var out bytes.Buffer
	result := w.loop(&tool.Call{Stdout: &out}, s, hook.Comms, []hook.Push{hook.Bus}, epoch, "0-0", func(int64) *tool.Out { return nil }, nil)
	assert.Equal(t, tool.OK, result.Status, out.String())
	require.Len(t, got, 1)
	assert.True(t, got[0].Wake)
	assert.Equal(t, "49 more messages; read the bus", got[0].Text)
	assert.NotContains(t, out.String(), "secret-body")
	assert.NotContains(t, out.String(), "secret-subject")
	assert.NotContains(t, out.String(), "own-body")
	assert.Contains(t, out.String(), "49 more messages; read the bus")
	assert.Contains(t, out.String(), "wake=true")
}

type claudeWaiter struct {
	bus.Store
	read func() []bus.Entry
}

func (s claudeWaiter) Tail(context.Context, string) (string, bool, error) { return "0-0", true, nil }
func (s claudeWaiter) BlockRead(context.Context, string, string, time.Duration, int) ([]bus.Entry, error) {
	return s.read(), nil
}
