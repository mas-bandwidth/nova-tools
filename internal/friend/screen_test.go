package friend

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractLastLines(t *testing.T) {
	t.Parallel()

	assert.Nil(t, ExtractLastLines("", 10))
	assert.Nil(t, ExtractLastLines("abc", 0))
	assert.Nil(t, ExtractLastLines("abc", -1))

	text := "one\ntwo\nthree\nfour\nfive\n"
	assert.Equal(t, []string{"four", "five"}, ExtractLastLines(text, 2))
	assert.Equal(t, []string{"one", "two", "three", "four", "five"}, ExtractLastLines(text, 10))

	withBlanks := "one\n\nthree\n"
	assert.Equal(t, []string{"one", "", "three"}, ExtractLastLines(withBlanks, 5))
}

func TestScreenResultFormatText(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)
	res := ScreenResult{
		Friend: "bob",
		Source: "tmux",
		Lines:  []string{"line 1", "line 2"},
		At:     at,
	}

	expected := "SCREEN friend=bob source=tmux lines=2 at=2026-10-04T03:00:00Z\n\nline 1\nline 2\n"
	assert.Equal(t, expected, res.FormatText())
}

func TestScreenResultJSON(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)
	res := ScreenResult{
		Friend: "bob",
		Source: "window",
		Lines:  []string{"alpha", "beta"},
		At:     at,
	}

	raw, err := json.Marshal(res)
	require.NoError(t, err)

	var data map[string]any
	require.NoError(t, json.Unmarshal(raw, &data))
	assert.Equal(t, "bob", data["friend"])
	assert.Equal(t, "window", data["source"])
	assert.Equal(t, "2026-10-04T03:00:00Z", data["at"])
	assert.Equal(t, []any{"alpha", "beta"}, data["lines"])
}

func TestScreenTmuxHosted(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)
	state := t.TempDir()
	require.NoError(t, WriteHost(state, Hosted{Session: "friend-bob", Harness: "aider"}))

	exec := func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
		assert.Equal(t, "tmux", name)
		assert.Equal(t, []string{"capture-pane", "-p", "-t", "friend-bob"}, args)
		return "row 1\nrow 2\nrow 3\nrow 4\n", 0, nil
	}

	res, err := Screen(context.Background(), ScreenOpts{
		Friend:   "bob",
		Lines:    2,
		StateDir: state,
		Now:      func() time.Time { return at },
		Exec:     exec,
	})
	require.NoError(t, err)
	assert.Equal(t, "bob", res.Friend)
	assert.Equal(t, "tmux", res.Source)
	assert.Equal(t, []string{"row 3", "row 4"}, res.Lines)
	assert.Equal(t, at, res.At)
}

func TestScreenTmuxNotRunning(t *testing.T) {
	t.Parallel()

	state := t.TempDir()
	require.NoError(t, WriteHost(state, Hosted{Session: "friend-bob", Harness: "aider"}))

	exec := func(_ context.Context, _, _ string, _ []string, _ string) (string, int, error) {
		return "can't find session friend-bob", 1, nil
	}

	_, err := Screen(context.Background(), ScreenOpts{
		Friend:   "bob",
		StateDir: state,
		Exec:     exec,
	})
	require.Error(t, err)
	var sr ScreenRefused
	require.True(t, errors.As(err, &sr))
	assert.Contains(t, sr.Why, "the tmux session friend-bob is not running")
	assert.Contains(t, sr.Remedy, "nova-friend host")
}

func TestScreenGUIWindowSuccess(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)
	state := t.TempDir()
	require.NoError(t, WriteStatus(state, Status{Friend: "bob", Harness: "antigravity", SessionLive: "chat-bob"}))

	reader := func(_ context.Context, bundle, target string) (string, error) {
		assert.Equal(t, AntigravityApp.Bundle, bundle)
		return "step 1\nstep 2\nstep 3\n", nil
	}

	res, err := Screen(context.Background(), ScreenOpts{
		Friend:       "bob",
		Lines:        2,
		StateDir:     state,
		Now:          func() time.Time { return at },
		WindowReader: reader,
	})
	require.NoError(t, err)
	assert.Equal(t, "bob", res.Friend)
	assert.Equal(t, "window", res.Source)
	assert.Equal(t, []string{"step 2", "step 3"}, res.Lines)
}

func TestScreenGUIWindowPermissionMissing(t *testing.T) {
	t.Parallel()

	state := t.TempDir()
	require.NoError(t, WriteStatus(state, Status{Friend: "bob", Harness: "antigravity", SessionLive: "chat-bob"}))

	reader := func(_ context.Context, _, _ string) (string, error) {
		return "", ErrAccessibilityPermission
	}

	_, err := Screen(context.Background(), ScreenOpts{
		Friend:       "bob",
		StateDir:     state,
		Binary:       "/opt/nova/bin/nova-friend",
		WindowReader: reader,
	})
	require.Error(t, err)
	var sr ScreenRefused
	require.True(t, errors.As(err, &sr))
	assert.Equal(t, "accessibility permission not granted", sr.Why)
	assert.Contains(t, sr.Remedy, "grant accessibility permission to /opt/nova/bin/nova-friend in System Settings > Privacy & Security > Accessibility")
}

func TestScreenHarnessWithNeither(t *testing.T) {
	t.Parallel()

	state := t.TempDir()
	require.NoError(t, WriteStatus(state, Status{Friend: "bob", Harness: "opencode"}))

	exec := func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
		return "can't find session", 1, nil
	}

	_, err := Screen(context.Background(), ScreenOpts{
		Friend:   "bob",
		StateDir: state,
		Exec:     exec,
	})
	require.Error(t, err)
	var sr ScreenRefused
	require.True(t, errors.As(err, &sr))
	assert.Contains(t, sr.Why, "opencode has no screen: it is neither hosted in tmux nor a GUI harness")
}

// TestScreenWindowReaderRequiresATextArea pins the accessibility read: it
// takes the window's text area alone, so a window without readable text is
// refused, never described by its title or description as if it were text
// (docs/SPEC-FRIEND.md, Screen: missing readable text is a refusal).
func TestScreenWindowReaderRequiresATextArea(t *testing.T) {
	t.Parallel()

	var script string
	run := func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
		require.Equal(t, "osascript", name)
		script = args[len(args)-1]
		return noWindowTextMarker, 1, nil
	}

	_, err := DefaultWindowReader(run)(context.Background(), AntigravityApp.Bundle, "chat-bob")
	require.ErrorIs(t, err, ErrNoWindowText)
	assert.Contains(t, script, "value of text area 1")
	assert.NotContains(t, script, "description of")
	assert.NotContains(t, script, "name of targetWin")
}

// TestScreenWindowReaderEscapesAQuotedTarget pins that the recorded directory
// or session title never reaches the AppleScript source unescaped: a target
// holding a double quote or a backslash is passed as an escaped string
// literal, so a valid directory such as /w/bob"notes compiles instead of
// failing with a syntax error (docs/SPEC-FRIEND.md, Screen: the window is
// found by the friend's recorded directory or session title).
func TestScreenWindowReaderEscapesAQuotedTarget(t *testing.T) {
	t.Parallel()

	var script string
	run := func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
		require.Equal(t, "osascript", name)
		script = args[len(args)-1]
		return "text\n", 0, nil
	}

	_, err := DefaultWindowReader(run)(context.Background(), AntigravityApp.Bundle, `/w/bob"notes`)
	require.NoError(t, err)
	assert.Contains(t, script, `contains "/w/bob\"notes"`)
	assert.NotContains(t, script, `contains "/w/bob"notes"`)

	_, err = DefaultWindowReader(run)(context.Background(), AntigravityApp.Bundle, `/w/bob\notes`)
	require.NoError(t, err)
	assert.Contains(t, script, `contains "/w/bob\\notes"`)
}

// TestScreenRefusesAWindowWithNoReadableText pins the seam's answer: a reader
// that finds no text area refuses, naming the missing text, rather than
// printing anything it found instead (docs/SPEC-FRIEND.md, Screen).
func TestScreenRefusesAWindowWithNoReadableText(t *testing.T) {
	t.Parallel()

	state := t.TempDir()
	require.NoError(t, WriteStatus(state, Status{Friend: "bob", Harness: "antigravity", SessionLive: "chat-bob"}))

	reader := func(_ context.Context, _, _ string) (string, error) {
		return "", ErrNoWindowText
	}

	_, err := Screen(context.Background(), ScreenOpts{
		Friend:       "bob",
		StateDir:     state,
		WindowReader: reader,
	})
	require.Error(t, err)
	var sr ScreenRefused
	require.True(t, errors.As(err, &sr))
	assert.Contains(t, sr.Why, "the matching bob window has no readable text")
}
