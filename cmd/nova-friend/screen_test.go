package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScreenPrintsTheLastLinesOfAHostedPane(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	state := filepath.Join(t.TempDir(), "bob")
	require.NoError(t, friend.WriteHost(state, friend.Hosted{Session: "friend-bob", Harness: "aider"}))

	var calls []string
	captureOutput := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	r.exec = func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "tmux" && len(args) >= 4 && args[0] == "capture-pane" {
			return captureOutput, 0, nil
		}
		return "", 0, nil
	}

	cli := r.cli()
	out := cli.Do(t, "screen", "bob", "--lines", "3", "--state-dir", state).Exit(0)
	assert.Contains(t, out.Stdout, "SCREEN friend=bob source=tmux lines=3 at=")
	assert.Contains(t, out.Stdout, "line 3\nline 4\nline 5")
	assert.Equal(t, []string{"tmux capture-pane -p -t friend-bob"}, calls)

	outAll := cli.Do(t, "screen", "bob", "--state-dir", state).Exit(0)
	assert.Contains(t, outAll.Stdout, "SCREEN friend=bob source=tmux lines=5 at=")
	assert.Contains(t, outAll.Stdout, "line 1\nline 2\nline 3\nline 4\nline 5")

	jsonOut := cli.Do(t, "screen", "bob", "--lines", "2", "--state-dir", state, "--json").Exit(0)
	var data map[string]any
	require.NoError(t, json.Unmarshal([]byte(jsonOut.Stdout), &data))
	assert.Equal(t, "bob", data["friend"])
	assert.Equal(t, "tmux", data["source"])
	assert.Equal(t, []any{"line 4", "line 5"}, data["lines"])
}

func TestScreenReadsAGUIWindowThroughTheAccessibilitySeam(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	state := filepath.Join(t.TempDir(), "bob")
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "antigravity", SessionLive: "chat-bob"}))

	var readBundle, readTarget string
	windowText := "step 1\nstep 2\nstep 3\nstep 4\n"
	r.windowReader = func(_ context.Context, bundle, target string) (string, error) {
		readBundle = bundle
		readTarget = target
		return windowText, nil
	}

	cli := r.cli()
	out := cli.Do(t, "screen", "bob", "--lines", "2", "--state-dir", state).Exit(0)
	assert.Contains(t, out.Stdout, "SCREEN friend=bob source=window lines=2 at=")
	assert.Contains(t, out.Stdout, "step 3\nstep 4")
	assert.Equal(t, friend.AntigravityApp.Bundle, readBundle)
	assert.NotEmpty(t, readTarget)

	jsonOut := cli.Do(t, "screen", "bob", "--lines", "2", "--state-dir", state, "--json").Exit(0)
	var data map[string]any
	require.NoError(t, json.Unmarshal([]byte(jsonOut.Stdout), &data))
	assert.Equal(t, "bob", data["friend"])
	assert.Equal(t, "window", data["source"])
	assert.Equal(t, []any{"step 3", "step 4"}, data["lines"])
}

// TestScreenUsesTheRecordedDirectoryForTheWindow pins the target the
// accessibility seam is asked for: the friend's recorded --dir, from her
// launch agent, wins even when her status carries no live session and even
// when --state-dir names the state directory (internal/friend/state.go:
// SessionLive is the mailbox's conversation, not the directory).
func TestScreenUsesTheRecordedDirectoryForTheWindow(t *testing.T) {
	t.Parallel()
	r := newRig(t)

	agent := friend.Agent{Friend: "bob", Home: r.home}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>ProgramArguments</key>
	<array>
		<string>/opt/nova/bin/nova-friend</string>
		<string>serve</string>
		<string>--as</string>
		<string>bob</string>
		<string>--dir</string>
		<string>/w/bob</string>
	</array>
</dict>
</plist>
`
	require.NoError(t, os.MkdirAll(filepath.Dir(agent.PlistPath()), 0o755))
	require.NoError(t, os.WriteFile(agent.PlistPath(), []byte(plist), 0o644))

	state := friend.DefaultStateDir(r.home, "bob")
	require.NoError(t, os.MkdirAll(state, 0o755))
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "antigravity"}))

	var readTarget string
	r.windowReader = func(_ context.Context, _, target string) (string, error) {
		readTarget = target
		return "step 1\nstep 2\n", nil
	}

	out := r.cli().Do(t, "screen", "bob", "--state-dir", state).Exit(0)
	assert.Contains(t, out.Stdout, "SCREEN friend=bob source=window")
	assert.Equal(t, "/w/bob", readTarget)
}

func TestScreenRefusesWithoutThePermission(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	state := filepath.Join(t.TempDir(), "bob")
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "antigravity", SessionLive: "chat-bob"}))

	r.windowReader = func(_ context.Context, _, _ string) (string, error) {
		return "", friend.ErrAccessibilityPermission
	}

	cli := r.cli()
	out := cli.Do(t, "screen", "bob", "--state-dir", state).Exit(1)
	assert.Contains(t, out.Stderr, "SCREEN REFUSED: accessibility permission not granted; run: grant accessibility permission")
	assert.Contains(t, out.Stderr, "in System Settings > Privacy & Security > Accessibility")
}

func TestScreenRefusesAHarnessWithNoScreen(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	state := filepath.Join(t.TempDir(), "bob")
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "opencode"}))

	r.exec = func(_ context.Context, _, _ string, _ []string, _ string) (string, int, error) {
		return "can't find session", 1, nil
	}

	cli := r.cli()
	out := cli.Do(t, "screen", "bob", "--state-dir", state).Exit(1)
	assert.Contains(t, out.Stderr, "SCREEN REFUSED: opencode has no screen: it is neither hosted in tmux nor a GUI harness")
}

func TestScreenValidatesFlagsAndArguments(t *testing.T) {
	t.Parallel()
	cli := newRig(t).cli()
	cli.Do(t, "screen").Exit(2).Err("SCREEN REFUSED: friend is required: nova-friend screen <friend>")
	cli.Do(t, "screen", "bob", "--lines", "0").Exit(2).Err("SCREEN REFUSED: --lines wants a positive integer")
	cli.Do(t, "screen", "bob", "--lines", "-1").Exit(2).Err("SCREEN REFUSED: --lines wants a positive integer")
}
