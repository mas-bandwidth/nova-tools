package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGrokNoticeFold(t *testing.T) {
	t.Parallel()
	var n grokNotice
	n.add(50)
	line, kind, got, ok := n.release(true)
	require.True(t, ok)
	assert.Equal(t, "50 more messages; read the bus", line)
	assert.Equal(t, "wake", kind)
	assert.Equal(t, 50, got)
	_, _, _, ok = n.release(true)
	assert.False(t, ok, "a second release while the line is outstanding writes nothing")

	n.add(7)
	n.observe(false, 1)
	n.observe(true, 1)
	line, kind, got, ok = n.release(true)
	require.True(t, ok)
	assert.Equal(t, "7 more messages; read the bus", line)
	assert.Equal(t, "wake", kind)
	assert.Equal(t, 7, got)

	n.observe(false, 2)
	n.observe(true, 2)
	n.add(1)
	line, kind, _, ok = n.release(false)
	require.True(t, ok)
	assert.Equal(t, "1 more message; read the bus", line)
	assert.Equal(t, "turn", kind)

	n.undo(1)
	assert.False(t, n.outstanding)
	assert.Equal(t, 1, n.waiting)
	line, _, _, ok = n.release(true)
	require.True(t, ok)
	assert.Equal(t, "1 more message; read the bus", line)

	// One read sees the witness and an idle word together. The busy sample
	// itself is already gone from the state word.
	var both grokNotice
	both.add(1)
	_, _, _, ok = both.release(true)
	require.True(t, ok)
	both.add(3)
	both.observe(true, 1)
	line, kind, _, ok = both.release(true)
	require.True(t, ok)
	assert.Equal(t, "3 more messages; read the bus", line)
	assert.Equal(t, "wake", kind)

	var stuck grokNotice
	stuck.add(1)
	_, _, _, ok = stuck.release(true)
	require.True(t, ok)
	stuck.add(5)
	stuck.observe(true, 0)
	_, _, _, ok = stuck.release(true)
	assert.False(t, ok, "an idle word with no new busy mark leaves the notice outstanding")
}

func TestGrokHarnessInstall(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	home := t.TempDir()
	res := r.cli().Run("hook", "--as", "ada", "--role", "comms", "--harness", "grok", "--grok-home", home, "--once")
	require.Equal(t, 1, res.Code, res.Stdout+res.Stderr)
	out := res.Stdout + res.Stderr
	assert.Contains(t, out, "HOOK STATE state=grok-installed")
	assert.Contains(t, out, "state=live")
	assert.Contains(t, out, "monitor `tail -n 0 -F ")
	assert.Equal(t, hook.StateLive, r.h.State(t, "ada").State)

	scriptPath := filepath.Join(home, "hooks", "nova-hook-grok.sh")
	script, err := os.ReadFile(scriptPath)
	require.NoError(t, err)
	assert.NotContains(t, string(script), "prove")
	info, err := os.Stat(scriptPath)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o111, "the hook script is executable")

	raw, err := os.ReadFile(filepath.Join(home, "hooks", "nova-hook.json"))
	require.NoError(t, err)
	var doc grokHookFile
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.NotEmpty(t, doc.Hooks.UserPromptSubmit)
	assert.Contains(t, doc.Hooks.UserPromptSubmit[0].Hooks[0].Command, "'busy'")
	require.NotEmpty(t, doc.Hooks.Notification)
	assert.Equal(t, "idle_prompt", doc.Hooks.Notification[0].Matcher)
	assert.Contains(t, doc.Hooks.Stop[0].Hooks[0].Command, "'idle'")

	wake := filepath.Join(home, "nova-hook.wake")
	info, err = os.Stat(wake)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assert.Contains(t, out, wake)
	require.NoError(t, os.WriteFile(wake, []byte("kept\n"), 0o600))
	_, err = installGrokHarness(home)
	require.NoError(t, err)
	again, err := os.ReadFile(wake)
	require.NoError(t, err)
	assert.Equal(t, "kept\n", string(again), "a second install leaves the wake file")
}

func TestGrokHarnessRefusals(t *testing.T) {
	t.Parallel()
	t.Run("home is a file", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		home := filepath.Join(t.TempDir(), "not-a-dir")
		require.NoError(t, os.WriteFile(home, []byte("x"), 0o600))
		res := r.cli().Run("hook", "--as", "ada", "--role", "comms", "--harness", "grok", "--grok-home", home, "--once")
		assert.Equal(t, 2, res.Code, res.Stdout+res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "could not be installed")
		assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
	})
	t.Run("dry-run writes nothing", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		home := filepath.Join(t.TempDir(), "fresh")
		res := r.cli().Run("hook", "--as", "ada", "--role", "comms", "--harness", "grok", "--grok-home", home, "--dry-run")
		assert.Zero(t, res.Code, res.Stdout+res.Stderr)
		assert.Contains(t, res.Stdout, "would install the grok harness into "+home)
		assert.NotContains(t, res.Stdout+res.Stderr, "grok-installed")
		_, err := os.Stat(home)
		assert.True(t, os.IsNotExist(err))
		assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
	})
	t.Run("unknown harness", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		res := r.cli().Run("hook", "--as", "ada", "--role", "comms", "--harness", "no-such", "--once")
		assert.Equal(t, 2, res.Code, res.Stdout+res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "--harness wants muse")
		assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
	})
	t.Run("codex flags", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		home := t.TempDir()
		res := r.cli().Run("hook", "--as", "ada", "--role", "comms", "--harness", "grok", "--grok-home", home, "--codex-socket", "/private/socket", "--once")
		assert.Equal(t, 2, res.Code, res.Stdout+res.Stderr)
		_, err := os.Stat(filepath.Join(home, "hooks"))
		assert.True(t, os.IsNotExist(err))
		assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
	})
	t.Run("member role", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		home := t.TempDir()
		res := r.cli().Run("hook", "--as", "ada", "--role", "member", "--harness", "grok", "--grok-home", home, "--once")
		assert.Equal(t, 2, res.Code, res.Stdout+res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "holds the bus push")
		_, err := os.Stat(filepath.Join(home, "hooks"))
		assert.True(t, os.IsNotExist(err))
		assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
	})
	t.Run("relative home", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		res := r.cli().Run("hook", "--as", "ada", "--role", "comms", "--harness", "grok", "--grok-home", "relative", "--once")
		assert.Equal(t, 2, res.Code, res.Stdout+res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "absolute path")
		assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
	})
	t.Run("line break", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		res := r.cli().Run("hook", "--as", "ada", "--role", "comms", "--harness", "grok", "--grok-home", filepath.Join(t.TempDir(), "bad\nname"), "--once")
		assert.Equal(t, 2, res.Code, res.Stdout+res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "line break")
		assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
	})
}

func TestGrokCommandQuotesShellMeta(t *testing.T) {
	t.Parallel()
	for _, frag := range []string{";", "&", "|", "\\", "'"} {
		t.Run(frag, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			marker := filepath.Join(dir, "marker")
			argsPath := filepath.Join(dir, "args")
			script := filepath.Join(dir, "hook.sh")
			require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" \"$2\" > '"+argsPath+"'\n"), 0o755))
			state := dir + "/s" + frag + "touch " + marker
			home, err := grokHome(state, nil)
			require.NoError(t, err)
			cmd := grokCommand(script, "busy", home).Command
			require.NoError(t, exec.Command("sh", "-c", cmd).Run(), cmd)
			_, err = os.Stat(marker)
			assert.True(t, os.IsNotExist(err), cmd)
			got, err := os.ReadFile(argsPath)
			require.NoError(t, err)
			assert.Equal(t, "busy\n"+home+"\n", string(got))
		})
	}
}

func TestGrokQuotedCommandKeepsTheTurnMark(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "a;b&c|d\\e")
	_, err := installGrokHarness(home)
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(home, "hooks", "nova-hook.json"))
	require.NoError(t, err)
	var doc grokHookFile
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.NoError(t, exec.Command("sh", "-c", doc.Hooks.UserPromptSubmit[0].Hooks[0].Command).Run())
	require.NoError(t, exec.Command("sh", "-c", doc.Hooks.Stop[0].Hooks[0].Command).Run())
	state, err := os.ReadFile(filepath.Join(home, "nova-hook", "state"))
	require.NoError(t, err)
	assert.Equal(t, "idle\n", string(state))
	turn, err := os.ReadFile(filepath.Join(home, "nova-hook", "turn"))
	require.NoError(t, err)
	assert.Equal(t, "x\n", string(turn))
	idle, gen, err := readGrokSample(home)
	require.NoError(t, err)
	assert.True(t, idle)
	assert.Equal(t, 1, gen)
}

// grokWaiter is the fake harness bus: BlockRead returns what the test says,
// and the clock is the rig's, which this test does not move.
type grokWaiter struct {
	bus.Store
	read func() []bus.Entry
}

func (s grokWaiter) Tail(context.Context, string) (string, bool, error) { return "0-0", true, nil }
func (s grokWaiter) BlockRead(context.Context, string, string, time.Duration, int) ([]bus.Entry, error) {
	return s.read(), nil
}

func grokMessages(n int, body string) []bus.Entry {
	out := make([]bus.Entry, n)
	for i := range out {
		m := bus.Message{ID: "m", From: "bob", Subject: "note", Body: body}
		out[i] = bus.Entry{Entry: "1-1", Fields: m.Fields()}
	}
	return out
}

func TestGrokHarnessBurstIsOneNotice(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	home := t.TempDir()
	wake, err := installGrokHarness(home)
	require.NoError(t, err)
	w := r.world()
	ctx, cancel := context.WithCancel(context.Background())
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
	w.grok = &grokRoute{home: home, wake: wake, as: "ada", role: hook.Comms}
	step := 0
	st := grokWaiter{Store: bus.Redis{C: r.h.Client}, read: func() []bus.Entry {
		step++
		switch step {
		case 1:
			return grokMessages(50, "payload-secret")
		case 2:
			return grokMessages(7, "later-secret")
		case 3:
			require.NoError(t, os.MkdirAll(filepath.Join(home, "nova-hook"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(home, "nova-hook", "state"), []byte("busy\n"), 0o600))
			return nil
		default:
			require.NoError(t, os.WriteFile(filepath.Join(home, "nova-hook", "state"), []byte("idle\n"), 0o600))
			cancel()
			return nil
		}
	}}
	epoch, _, err := r.h.Store.HookAs(ctx, "ada", hook.Comms, "test")
	require.NoError(t, err)
	s := &session{bus: st, hooks: r.h.Store, as: "ada"}
	var out bytes.Buffer
	result := w.loop(&tool.Call{Stdout: &out}, s, hook.Comms, []hook.Push{hook.Bus}, epoch, "0-0", func(int64) *tool.Out { return nil }, nil)
	assert.Equal(t, tool.OK, result.Status, out.String())
	body, err := os.ReadFile(wake)
	require.NoError(t, err)
	assert.Equal(t, "50 more messages; read the bus\n7 more messages; read the bus\n", string(body))
	assert.NotContains(t, string(body), "payload-secret")
	assert.NotContains(t, string(body), "later-secret")
	assert.NotContains(t, out.String(), "payload-secret")
	assert.Equal(t, 2, strings.Count(out.String(), "kind=wake"))
	assert.NotContains(t, out.String(), "HOOK PUSH push=bus")
}

// TestGrokBusyThenIdleBetweenFlushesStillDelivers writes busy and then idle
// into the state file between two flushes. The idle write replaces the word.
// The turn mark from the busy write is still there, and the later message is
// delivered.
func TestGrokBusyThenIdleBetweenFlushesStillDelivers(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	home := t.TempDir()
	wake, err := installGrokHarness(home)
	require.NoError(t, err)
	script := filepath.Join(home, "hooks", "nova-hook-grok.sh")
	stateDir := filepath.Join(home, "nova-hook")
	w := r.world()
	ctx, cancel := context.WithCancel(context.Background())
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
	w.grok = &grokRoute{home: home, wake: wake, as: "ada", role: hook.Comms}
	step := 0
	st := grokWaiter{Store: bus.Redis{C: r.h.Client}, read: func() []bus.Entry {
		step++
		if step == 1 {
			return grokMessages(1, "first-secret")
		}
		require.NoError(t, exec.Command(script, "busy", stateDir).Run())
		require.NoError(t, exec.Command(script, "idle", stateDir).Run())
		word, err := os.ReadFile(filepath.Join(stateDir, "state"))
		require.NoError(t, err)
		assert.Equal(t, "idle\n", string(word))
		mark, err := os.ReadFile(filepath.Join(stateDir, "turn"))
		require.NoError(t, err)
		assert.Equal(t, "x\n", string(mark))
		cancel()
		return grokMessages(1, "second-secret")
	}}
	epoch, _, err := r.h.Store.HookAs(ctx, "ada", hook.Comms, "test")
	require.NoError(t, err)
	s := &session{bus: st, hooks: r.h.Store, as: "ada"}
	var out bytes.Buffer
	result := w.loop(&tool.Call{Stdout: &out}, s, hook.Comms, []hook.Push{hook.Bus}, epoch, "0-0", func(int64) *tool.Out { return nil }, nil)
	assert.Equal(t, tool.OK, result.Status, out.String())
	body, err := os.ReadFile(wake)
	require.NoError(t, err)
	assert.Equal(t, "1 more message; read the bus\n1 more message; read the bus\n", string(body))
	assert.NotContains(t, string(body), "first-secret")
	assert.NotContains(t, string(body), "second-secret")
	assert.Equal(t, 2, strings.Count(out.String(), "kind=wake"))
}

func TestGrokHarnessDeliversIntoARunningTurn(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	home := t.TempDir()
	wake, err := installGrokHarness(home)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(home, "nova-hook"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, "nova-hook", "state"), []byte("busy\n"), 0o600))
	w := r.world()
	ctx, cancel := context.WithCancel(context.Background())
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
	w.grok = &grokRoute{home: home, wake: wake, as: "ada", role: hook.Comms}
	step := 0
	st := grokWaiter{Store: bus.Redis{C: r.h.Client}, read: func() []bus.Entry {
		step++
		if step > 1 {
			cancel()
			return nil
		}
		return grokMessages(1, "payload-secret")
	}}
	epoch, _, err := r.h.Store.HookAs(ctx, "ada", hook.Comms, "test")
	require.NoError(t, err)
	s := &session{bus: st, hooks: r.h.Store, as: "ada"}
	var out bytes.Buffer
	result := w.loop(&tool.Call{Stdout: &out}, s, hook.Comms, []hook.Push{hook.Bus}, epoch, "0-0", func(int64) *tool.Out { return nil }, nil)
	assert.Equal(t, tool.OK, result.Status, out.String())
	body, err := os.ReadFile(wake)
	require.NoError(t, err)
	assert.Equal(t, "1 more message; read the bus\n", string(body))
	assert.NotContains(t, string(body), "payload-secret")
	assert.Contains(t, out.String(), "kind=turn")
	assert.NotContains(t, out.String(), "kind=wake")
}

func TestGrokWakeWriteFailureStopsWithoutUnhook(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	home := t.TempDir()
	w := r.world()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
	w.grok = &grokRoute{home: home, wake: filepath.Join(home, "missing-wake"), as: "ada", role: hook.Comms}
	st := grokWaiter{Store: bus.Redis{C: r.h.Client}, read: func() []bus.Entry {
		return grokMessages(3, "payload-secret")
	}}
	epoch, _, err := r.h.Store.HookAs(ctx, "ada", hook.Comms, "test")
	require.NoError(t, err)
	require.NoError(t, r.h.Store.Renew(ctx, "ada", hook.Comms, hook.Bus, epoch, "test"))
	s := &session{bus: st, hooks: r.h.Store, as: "ada"}
	var out bytes.Buffer
	result := w.loop(&tool.Call{Stdout: &out}, s, hook.Comms, []hook.Push{hook.Bus}, epoch, "0-0", func(int64) *tool.Out { return nil }, nil)
	require.NotNil(t, result)
	assert.Equal(t, tool.Failed, result.Status, out.String())
	assert.Equal(t, 3, w.grok.note.waiting)
	assert.False(t, w.grok.note.outstanding)
	assert.Equal(t, hook.StateLive, r.h.State(t, "ada").State)
	assert.Equal(t, epoch, r.h.State(t, "ada").Epoch)
	assert.NotContains(t, out.String(), "payload-secret")
}
