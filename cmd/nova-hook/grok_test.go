package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	var doc struct {
		Hooks map[string][]struct {
			Matcher string
			Hooks   []struct{ Command string }
		}
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.NotEmpty(t, doc.Hooks["UserPromptSubmit"])
	assert.Contains(t, doc.Hooks["UserPromptSubmit"][0].Hooks[0].Command, "'busy'")
	require.NotEmpty(t, doc.Hooks["Notification"])
	assert.Equal(t, "idle_prompt", doc.Hooks["Notification"][0].Matcher)
	assert.Contains(t, doc.Hooks["Stop"][0].Hooks[0].Command, "'idle'")

	wake := filepath.Join(home, "nova-hook.wake")
	info, err = os.Stat(wake)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assert.Contains(t, out, wake)
	require.NoError(t, os.WriteFile(wake, []byte("kept\n"), 0o600))
	_, err = notice.InstallGrok(home)
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
