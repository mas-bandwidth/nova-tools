package friend

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClaude writes a claude binary that records its argv and its
// CLAUDE_CONFIG_DIR beside itself and prints a stream-json run: an init, an
// assistant line, the rate_limit_event and the result with its cost.
func fakeClaude(t *testing.T, event string, cost string) (program, record string) {
	t.Helper()
	dir := t.TempDir()
	record = filepath.Join(dir, "calls")
	program = filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		"echo \"$CLAUDE_CONFIG_DIR|$*\" >> " + strconv.Quote(record) + "\n" +
		"echo '{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"s1\"}'\n" +
		"echo '{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"ready\"}]}}'\n" +
		"echo '" + event + "'\n" +
		"echo '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"total_cost_usd\":" + cost + ",\"session_id\":\"s1\",\"result\":\"ready\"}'\n"
	require.NoError(t, os.WriteFile(program, []byte(script), 0o755))
	return program, record
}

// A headless Claude Code account is a lane harness (docs/SPEC-FRIEND.md,
// one-shot lanes, the Claude lanes): a session is opened by a trimmed
// `claude -p` run (the measured 50k to 12.7k tokens a call), each card is a
// --resume turn in it with CLAUDE_CONFIG_DIR set to the friend's own, every
// run's cost is summed from its stream-json result, and the rate_limit_event
// is read for the five-hour and weekly utilization; a rejected one stops the
// lanes until its resetsAt (UsageLimited) with no shell script.
func TestAHeadlessClaudeLaneRunsACardPricesItAndReadsItsLimit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 18, 30, 0, 0, time.UTC)
	fiveReset, sevenReset := now.Add(90*time.Minute), now.Add(50*time.Hour)
	program, record := fakeClaude(t, claudeEvent("allowed", false, 0.42, 0.10, fiveReset, sevenReset), "0.0125")
	cfg := t.TempDir()
	wantCfg := cfg + "|"
	var out strings.Builder
	c := &Claude{Dir: t.TempDir(), ConfigDir: cfg, Run: RealExec, Program: program, Out: &out, Now: func() time.Time { return now }}

	id, err := c.OpenSession(t.Context(), "You are bob.")
	require.NoError(t, err)
	require.NotEmpty(t, id)
	lt, err := c.DeliverTo(t.Context(), id, "card c1")
	require.NoError(t, err)
	assert.Zero(t, lt.Exit)

	raw, err := os.ReadFile(record)
	require.NoError(t, err)
	calls := strings.Split(strings.TrimSpace(string(raw)), "\n")
	require.Len(t, calls, 2)
	trimmed := "--strict-mcp-config --disable-slash-commands --no-chrome --tools Bash Read Write Edit Grep Glob"
	for _, call := range calls {
		assert.True(t, strings.HasPrefix(call, wantCfg), "the friend's own config directory: %s", call)
		assert.Contains(t, call, "-p ")
		assert.Contains(t, call, "--output-format stream-json --verbose")
		assert.Contains(t, call, trimmed)
	}
	assert.Contains(t, calls[0], "--session-id "+id, "a new session is named by its first run")
	assert.Contains(t, calls[1], "--resume "+id, "a card is a turn in the lane's session")
	assert.True(t, strings.HasSuffix(calls[1], "card c1"))

	cost, usage := c.Spent()
	assert.InDelta(t, 0.025, cost, 1e-9, "both runs priced from their results")
	assert.InDelta(t, 0.42, usage.FiveHour, 1e-9)
	assert.InDelta(t, 0.10, usage.SevenDay, 1e-9)
	assert.True(t, usage.FiveHourResets.Equal(fiveReset))
	assert.Contains(t, out.String(), "claude: cost=$0.0125 total=$0.0250 five_hour=0.42 seven_day=0.10")

	// a rejected event is a usage limit until its reset, never a backoff
	program, _ = fakeClaude(t, claudeEvent("rejected", false, 1.0, 0.10, fiveReset, sevenReset), "0.0010")
	c = &Claude{Dir: t.TempDir(), ConfigDir: cfg, Run: RealExec, Program: program, Now: func() time.Time { return now }}
	_, err = c.DeliverTo(t.Context(), "s1", "card c2")
	var limited UsageLimited
	require.ErrorAs(t, err, &limited)
	assert.True(t, limited.Until.Equal(fiveReset), "until resetsAt: %s", limited.Until)
	assert.Equal(t, "s1", limited.Session)
}

// A usage limit pauses the lanes until its reset and lowers no cap: the
// reset is the stream's resetsAt, not a backoff and not a guess.
func TestAUsageLimitPausesTheLanesUntilItsResetAndLowersNoCap(t *testing.T) {
	t.Parallel()
	g := &LaneGovernor{}
	now := time.Date(2026, 10, 4, 18, 30, 0, 0, time.UTC)
	until := now.Add(3 * time.Hour)
	assert.NotEmpty(t, g.PauseUntil(until, "claude rate_limit_event rejected"))
	assert.True(t, g.Paused(now.Add(179*time.Minute)))
	assert.False(t, g.Paused(until))
	assert.Equal(t, 8, g.Cap(8), "no cap is lowered")
	assert.Empty(t, g.PauseUntil(now.Add(time.Hour), "an earlier reset"), "a pause already past it changes nothing")
}

// A bud's reader runs on the same account the same way (docs/SPEC-FRIEND.md,
// the Claude lanes, the reader row): a read is one trimmed `claude -p` in a
// session of its own (--session-id), on the model of the read's tier placed
// before --tools (which takes every argument after it), priced from its
// result and its limit read, so a claude daemon's reader row needs no script.
func TestAHeadlessClaudeRunsAReadAsOneShotOnItsTiersModelPricedLikeACard(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 18, 30, 0, 0, time.UTC)
	fiveReset, sevenReset := now.Add(90*time.Minute), now.Add(50*time.Hour)
	program, record := fakeClaude(t, claudeEvent("allowed", false, 0.42, 0.10, fiveReset, sevenReset), "0.0300")
	cfg := t.TempDir()
	var out strings.Builder
	c := &Claude{Dir: t.TempDir(), ConfigDir: cfg, Run: RealExec, Program: program, Out: &out, Now: func() time.Time { return now }}
	var reads ReadHarness = c

	lt, err := reads.RunRead(t.Context(), ReadModels["heavy"], "read the card")
	require.NoError(t, err)
	assert.Zero(t, lt.Exit)
	_, err = reads.RunRead(t.Context(), "", "read again")
	require.NoError(t, err)

	raw, err := os.ReadFile(record)
	require.NoError(t, err)
	calls := strings.Split(strings.TrimSpace(string(raw)), "\n")
	require.Len(t, calls, 2)
	for _, call := range calls {
		assert.True(t, strings.HasPrefix(call, cfg+"|"), "the friend's own config directory: %s", call)
		assert.Contains(t, call, "-p --output-format stream-json --verbose")
		assert.Contains(t, call, "--session-id ", "a read is a session of its own")
		assert.NotContains(t, call, "--resume")
	}
	assert.Contains(t, calls[0], "--verbose --model claude-opus-5-5 --strict-mcp-config", "the tier's model, before --tools")
	assert.True(t, strings.HasSuffix(calls[0], " read the card"))
	assert.NotContains(t, calls[1], "--model", "no model is the account's own")
	cost, _ := c.Spent()
	assert.InDelta(t, 0.06, cost, 1e-9, "every read priced from its result")
	assert.Contains(t, out.String(), "claude: cost=$0.0300 total=$0.0600 five_hour=0.42")

	program, _ = fakeClaude(t, claudeEvent("rejected", false, 1.0, 0.10, fiveReset, sevenReset), "0.0010")
	c = &Claude{Dir: t.TempDir(), ConfigDir: cfg, Run: RealExec, Program: program, Now: func() time.Time { return now }}
	_, err = c.RunRead(t.Context(), "", "read at the limit")
	var limited UsageLimited
	require.ErrorAs(t, err, &limited, "a read at the limit is the lanes' pause until the reset")
	assert.True(t, limited.Until.Equal(fiveReset))
}
