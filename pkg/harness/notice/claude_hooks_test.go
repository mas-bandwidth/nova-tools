package notice

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pendingChallenge saves a notice holding nonce's canonical challenge for bob
// and names it pending, as the Claude shim's Push does.
func pendingChallenge(t *testing.T, state, nonce string) string {
	t.Helper()
	var e Envelope
	e.LocalReceiver.Identity, e.LocalReceiver.Redis = "bob", "127.0.0.1:6381"
	if nonce != "" {
		e.Challenge = &bus.Message{Subject: challengePrefix + nonce, Body: ChallengeText("bob", nonce, 5*time.Minute)}
	}
	e.Messages = 1
	path := filepath.Join(state, "notices", "n-"+nonce+".json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	raw, err := json.Marshal(e)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	_, err = (&Claude{State: state}).Push(context.Background(), Notice{Path: path})
	require.NoError(t, err)
	return path
}

func generation(t *testing.T, state string) int {
	t.Helper()
	n, err := ClaudeGeneration(state)
	require.NoError(t, err)
	return n
}

// The Stop hook (tla/ClaudeHookNotice.tla, SessionStop): an unproved pending
// challenge blocks the stop once, naming the prove line; a watcher missing
// blocks once more; then the turn ends and the generation advances. The
// generation never moves on a blocked stop.
func TestTheClaudeStopBlocksOnceForAChallengeThenOnceForAWatcher(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	path := pendingChallenge(t, state, "abc123")

	d, err := ClaudeStop(state, "/s/watch.sh")
	require.NoError(t, err)
	assert.True(t, d.Block)
	assert.Contains(t, d.Reason, "nova-hook prove --as bob --nonce abc123")
	assert.NotContains(t, d.Reason, "127.0.0.1:6381", "the store's address stays in the notice file")
	assert.Contains(t, d.Reason, path)
	assert.Equal(t, 0, generation(t, state), "a blocked stop is no turn boundary")

	d, err = ClaudeStop(state, "/s/watch.sh")
	require.NoError(t, err)
	assert.True(t, d.Block, "no watcher holds the lock")
	assert.Contains(t, d.Reason, "Monitor tool")
	assert.Contains(t, d.Reason, "/s/watch.sh")
	assert.NotContains(t, d.Reason, "hook challenge abc123", "the challenge is blocked once only")

	d, err = ClaudeStop(state, "/s/watch.sh")
	require.NoError(t, err)
	assert.False(t, d.Block, "each block happens once: the model chose")
	assert.Equal(t, 1, d.Generation)
}

// A proved challenge never blocks (BlockOnlyUnproved), and with a watcher
// armed the stop ends the turn at once.
func TestTheClaudeStopLetsAProvedChallengeAndAWatchedSessionEnd(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	pendingChallenge(t, state, "abc123")
	require.NoError(t, ClaudeProved(state, "abc123"))
	l, err := ClaudeWatch(state)
	require.NoError(t, err)
	defer func() { require.NoError(t, l.Unlock()) }()

	watched, err := ClaudeWatched(state)
	require.NoError(t, err)
	assert.True(t, watched)
	d, err := ClaudeStop(state, "/s/watch.sh")
	require.NoError(t, err)
	assert.False(t, d.Block)
	assert.Equal(t, 1, d.Generation)
}

// A newer challenge folded into the pending file blocks again: the block is
// once per nonce, not once per notice (IdleKnows).
func TestANewerChallengeBlocksTheStopAgain(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	l, err := ClaudeWatch(state)
	require.NoError(t, err)
	defer func() { require.NoError(t, l.Unlock()) }()
	pendingChallenge(t, state, "aaa111")
	d, err := ClaudeStop(state, "w")
	require.NoError(t, err)
	require.True(t, d.Block)
	pendingChallenge(t, state, "bbb222")
	d, err = ClaudeStop(state, "w")
	require.NoError(t, err)
	assert.True(t, d.Block)
	assert.Contains(t, d.Reason, "bbb222")
}

// With no watcher, a messages-only notice was never shown: the one watcher
// block names it before the turn's end consumes it.
func TestTheWatcherBlockNamesAnUnseenNotice(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	path := pendingChallenge(t, state, "")
	d, err := ClaudeStop(state, "w")
	require.NoError(t, err)
	require.True(t, d.Block)
	assert.Contains(t, d.Reason, path)
}

// Messages without a challenge never block a watched session: the Monitor told them.
func TestAPendingNoticeWithoutAChallengeNeverBlocks(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	l, err := ClaudeWatch(state)
	require.NoError(t, err)
	defer func() { require.NoError(t, l.Unlock()) }()
	pendingChallenge(t, state, "")
	d, err := ClaudeStop(state, "w")
	require.NoError(t, err)
	assert.False(t, d.Block)
}

// PostToolUse (Tool) surfaces an unproved pending challenge once, never a
// proved one, and says nothing with no notice pending.
func TestTheClaudeToolUseSurfacesAChallengeOnce(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	text, err := ClaudeToolUse(state)
	require.NoError(t, err)
	assert.Empty(t, text)

	pendingChallenge(t, state, "abc123")
	text, err = ClaudeToolUse(state)
	require.NoError(t, err)
	assert.Contains(t, text, "nova-hook prove --as bob --nonce abc123")
	text, err = ClaudeToolUse(state)
	require.NoError(t, err)
	assert.Empty(t, text, "once per nonce")

	pendingChallenge(t, state, "def456")
	require.NoError(t, ClaudeProved(state, "def456"))
	text, err = ClaudeToolUse(state)
	require.NoError(t, err)
	assert.Empty(t, text, "a proved challenge is never surfaced")
}

// SessionStart (Start) names the watcher line and the pending notice, and a
// consumed notice (Clear) is no longer pending.
func TestTheClaudeStartTellsTheWatcherAndThePendingNotice(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	text, err := ClaudeStart(state, "/s/watch.sh")
	require.NoError(t, err)
	assert.Contains(t, text, "/s/watch.sh")
	assert.NotContains(t, text, "pending")

	path := pendingChallenge(t, state, "abc123")
	text, err = ClaudeStart(state, "/s/watch.sh")
	require.NoError(t, err)
	assert.Contains(t, text, path)
	assert.Contains(t, text, "nova-hook prove --as bob --nonce abc123")

	require.NoError(t, (&Claude{State: state}).Clear(context.Background(), Notice{}))
	p, err := ReadClaudePending(state)
	require.NoError(t, err)
	assert.Equal(t, ClaudePending{}, p)
}

// SessionStart clears a spent missing-watcher block, so the next stop with no
// watcher is told again (Start resets nagged).
func TestTheClaudeStartResetsTheWatcherBlock(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	d, err := ClaudeStop(state, "w")
	require.NoError(t, err)
	require.True(t, d.Block)
	_, err = ClaudeStart(state, "w")
	require.NoError(t, err)
	d, err = ClaudeStop(state, "w")
	require.NoError(t, err)
	assert.True(t, d.Block)
}

// After a compaction the Monitor still runs: SessionStart says a watcher is
// armed rather than asking for a second one.
func TestTheClaudeStartSeesAWatcherThatStillRuns(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	l, err := ClaudeWatch(state)
	require.NoError(t, err)
	defer func() { require.NoError(t, l.Unlock()) }()
	text, err := ClaudeStart(state, "/s/watch.sh")
	require.NoError(t, err)
	assert.Contains(t, text, "a watcher is armed")
	assert.NotContains(t, text, "arm the watcher now")
}

// Two watchers never hold the lock at once.
func TestOneClaudeWatcherAtATime(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	l, err := ClaudeWatch(state)
	require.NoError(t, err)
	_, err = ClaudeWatch(state)
	require.Error(t, err)
	require.NoError(t, l.Unlock())
	l, err = ClaudeWatch(state)
	require.NoError(t, err)
	require.NoError(t, l.Unlock())
}

// The hook input names the event; empty input is the Stop it always was.
func TestReadClaudeEvent(t *testing.T) {
	t.Parallel()
	e, err := ReadClaudeEvent(strings.NewReader(""))
	require.NoError(t, err)
	assert.Equal(t, "Stop", e.Name)
	e, err = ReadClaudeEvent(strings.NewReader(`{"hook_event_name":"SessionStart","source":"compact"}`))
	require.NoError(t, err)
	assert.Equal(t, ClaudeEvent{Name: "SessionStart", Source: "compact"}, e)
	_, err = ReadClaudeEvent(strings.NewReader("not json"))
	require.Error(t, err)
	// a PostToolUse input carries the tool's whole response: read past MaxSaved
	big := `{"hook_event_name":"PostToolUse","tool_response":{"stdout":"` + strings.Repeat("x", 3*MaxSaved) + `"}}`
	e, err = ReadClaudeEvent(strings.NewReader(big))
	require.NoError(t, err)
	assert.Equal(t, "PostToolUse", e.Name)
}

// The install writes both scripts and the boundary command once under each
// event, keeping the file's other keys; installing again changes nothing.
func TestInstallClaudeWiresThreeEventsOnce(t *testing.T) {
	t.Parallel()
	state, settings := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(settings, "settings.json"), []byte(`{"model":"m","hooks":{"Stop":[{"hooks":[{"type":"command","command":"other"}]}]}}`), 0o600))
	require.NoError(t, InstallClaude("/bin/nova-hook", "bob", "friend", state, settings))
	require.NoError(t, InstallClaude("/bin/nova-hook", "bob", "friend", state, settings))
	raw, err := os.ReadFile(filepath.Join(settings, "settings.json"))
	require.NoError(t, err)
	var got struct {
		Model string                      `json:"model"`
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "m", got.Model)
	assert.Len(t, got.Hooks["Stop"], 2, "the other Stop hook stays; ours is added once")
	assert.Len(t, got.Hooks["PostToolUse"], 1)
	assert.Len(t, got.Hooks["SessionStart"], 1)
	watch, err := os.ReadFile(ClaudeWatchScript(settings))
	require.NoError(t, err)
	assert.Contains(t, string(watch), "--claude-watch")
	boundary, err := os.ReadFile(filepath.Join(settings, "nova-hook-claude-boundary.sh"))
	require.NoError(t, err)
	assert.Contains(t, string(boundary), "--claude-boundary")
	var env struct {
		Env map[string]string `json:"env"`
	}
	require.NoError(t, json.Unmarshal(raw, &env))
	assert.Equal(t, state, env.Env[ClaudeStateEnv], "the session's prove finds the state directory the hooks read")
}
