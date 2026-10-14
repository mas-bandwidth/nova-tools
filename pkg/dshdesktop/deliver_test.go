package dshdesktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func route(t *testing.T) Route {
	t.Helper()
	dir := t.TempDir()
	return Route{EventsDir: dir, HooksFile: filepath.Join(dir, "hooks.json")}
}

func readPending(t *testing.T, r Route) pending {
	t.Helper()
	path, err := PendingPath(r.EventsDir)
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var p pending
	require.NoError(t, json.Unmarshal(raw, &p))
	return p
}

func TestDeliverCoalescesABurstIntoOnePendingNotice(t *testing.T) {
	t.Parallel()
	r := route(t)
	for i := 0; i < 50; i++ {
		text, err := Deliver(r, "event-1", BusNotice)
		require.NoError(t, err)
		if i == 49 {
			assert.Contains(t, text, "50 messages available; read the bus")
		}
	}
	p := readPending(t, r)
	assert.Equal(t, 50, p.Count)
	assert.Contains(t, p.HookSpecificOutput.AdditionalContext, BusNotice)
	assert.Contains(t, p.HookSpecificOutput.AdditionalContext, "event-1")
	assert.Equal(t, "PostToolUse", p.HookSpecificOutput.HookEventName)
}

func TestDeliverFirstNoticeHasNoCount(t *testing.T) {
	t.Parallel()
	r := route(t)
	text, err := Deliver(r, "event-2", ChallengeNotice)
	require.NoError(t, err)
	assert.Contains(t, text, ChallengeNotice)
	assert.NotContains(t, text, "messages available")
	assert.Equal(t, 1, readPending(t, r).Count)
}

func TestDeliverRefusesExternalNoticeAndBadInputs(t *testing.T) {
	t.Parallel()
	r := route(t)
	_, err := Deliver(r, "e-1", "untrusted text")
	assert.ErrorIs(t, err, ErrPreSend)

	_, err = Deliver(Route{}, "e-1", BusNotice)
	assert.ErrorIs(t, err, ErrPreSend)
}

func TestChallengeIsAlwaysVisibleOverAnOrdinarySummary(t *testing.T) {
	t.Parallel()
	r := route(t)
	_, err := Deliver(r, "ordinary-1", BusNotice)
	require.NoError(t, err)
	// A challenge overrides the ordinary summary and is the visible notice.
	text, err := Deliver(r, "challenge-1", ChallengeNotice)
	require.NoError(t, err)
	assert.Contains(t, text, ChallengeNotice)
	assert.Contains(t, text, "challenge-1")
	p := readPending(t, r)
	assert.Equal(t, "challenge-1", p.ChallengeEventID)

	// A later ordinary message folds beside the challenge, never behind it.
	text, err = Deliver(r, "ordinary-2", BusNotice)
	require.NoError(t, err)
	assert.Contains(t, text, ChallengeNotice, "the challenge stays visible")
	assert.Contains(t, text, "challenge-1")
	assert.Contains(t, text, "ordinary messages available; read the bus")
	assert.Equal(t, "challenge-1", readPending(t, r).ChallengeEventID)
}

func TestNewerChallengeOverridesTheOlderOne(t *testing.T) {
	t.Parallel()
	r := route(t)
	_, err := Deliver(r, "challenge-1", ChallengeNotice)
	require.NoError(t, err)
	text, err := Deliver(r, "challenge-2", ChallengeNotice)
	require.NoError(t, err)
	assert.Contains(t, text, "challenge-2")
	assert.NotContains(t, text, "challenge-1")
	assert.Equal(t, "challenge-2", readPending(t, r).ChallengeEventID)
}

func TestInstallWritesHookConfig(t *testing.T) {
	t.Parallel()
	r := route(t)
	require.NoError(t, Install(r))
	raw, err := os.ReadFile(r.HooksFile)
	require.NoError(t, err)
	var cfg hookConfig
	require.NoError(t, json.Unmarshal(raw, &cfg))
	require.Len(t, cfg.Hooks.PostToolUse, 1)
	require.Len(t, cfg.Hooks.PostToolUse[0].Hooks, 1)
	assert.Equal(t, "command", cfg.Hooks.PostToolUse[0].Hooks[0].Type)
	assert.Contains(t, cfg.Hooks.PostToolUse[0].Hooks[0].Command, "cat ")
	require.Len(t, cfg.Hooks.Stop, 1)
	assert.Contains(t, cfg.Hooks.Stop[0].Hooks[0].Command, "continue")
}
