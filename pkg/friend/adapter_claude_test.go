//go:build unix

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

// fakeClaudeStream writes a claude binary that records its CLAUDE_CONFIG_DIR
// and argv beside itself, one line a run, prints a stream-json run (an init,
// an assistant line, the rate_limit_event and the result with its cost) and
// writes outbox's REPORT.md and RESULT.md when outbox is set.
func fakeClaudeStream(t *testing.T, event, cost, outbox string) (program, record string) {
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
	if outbox != "" {
		script += "mkdir -p '" + outbox + "' && echo 'Verdict: LAND' > '" + outbox + "/REPORT.md' && echo 'RESULT: c1' > '" + outbox + "/RESULT.md'\n"
	}
	require.NoError(t, os.WriteFile(program, []byte(script), 0o755))
	return program, record
}

func calls(t *testing.T, record string) []string {
	t.Helper()
	raw, err := os.ReadFile(record)
	require.NoError(t, err)
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// A headless Claude Code account's one-shot lane (docs/SPEC-FRIEND.md,
// one-shot lanes, the Claude lanes) runs each card as a trimmed `claude -p`
// (the measured 50k to 12.7k tokens a call) with CLAUDE_CONFIG_DIR set to the
// friend's own, prices every run from its stream-json result, and reads the
// rate_limit_event for the five-hour and weekly utilization; a rejected one
// stops the lanes until its resetsAt (UsageLimited) with no shell script.
func TestAHeadlessClaudeLaneRunsACardPricesItAndReadsItsLimit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 18, 30, 0, 0, time.UTC)
	fiveReset, sevenReset := now.Add(90*time.Minute), now.Add(50*time.Hour)
	dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
	card := func(id string) Card {
		c := Card{ID: id, Brief: filepath.Join(dir, "inbox", id+"~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", id+"~15")}
		require.NoError(t, os.WriteFile(c.Brief, []byte("STATUS: nova-sprint card "+id+", epoch 15"), 0o644)) // one line: the fake records each run on one
		return c
	}
	c1 := card("c1")
	program, record := fakeClaudeStream(t, claudeEvent("allowed", false, 0.42, 0.10, fiveReset, sevenReset), "0.0125", c1.Outbox)
	cfg := t.TempDir()
	var out strings.Builder
	cl := NewClaude("bob", dir, RealExec, &out)
	cl.Program, cl.Now, cl.ConfigDir = program, func() time.Time { return now }, func() string { return cfg }
	var _ CardRunner = cl

	lt, err := cl.RunCard(t.Context(), c1)
	require.NoError(t, err)
	assert.Zero(t, lt.Exit)
	_, err = cl.RunCard(t.Context(), c1)
	require.NoError(t, err)

	got := calls(t, record)
	require.Len(t, got, 2)
	for _, call := range got {
		assert.True(t, strings.HasPrefix(call, cfg+"|-p STATUS: nova-sprint card c1"), "the friend's own config directory, the brief the prompt: %s", call)
		assert.True(t, strings.HasSuffix(call, "--output-format stream-json --verbose --strict-mcp-config --disable-slash-commands --no-chrome --tools Bash Read Write Edit Grep Glob"), "the trimmed call, --tools last: %s", call)
	}

	cost, usage := cl.Spent()
	assert.InDelta(t, 0.025, cost, 1e-9, "both runs priced from their results")
	assert.InDelta(t, 0.42, usage.FiveHour, 1e-9)
	assert.InDelta(t, 0.10, usage.SevenDay, 1e-9)
	assert.True(t, usage.FiveHourResets.Equal(fiveReset))
	assert.True(t, usage.SevenDayResets.Equal(sevenReset))
	assert.Contains(t, out.String(), "claude: run=c1 cost=$0.0125 total=$0.0250 five_hour=0.42 seven_day=0.10 five_hour_resets=2026-10-04T20:00:00Z")
	var spender Spender = cl
	assert.Equal(t, "spend: harness=claude runs=2 cost_usd=0.0250 five_hour=0.42 seven_day=0.10 five_hour_resets=2026-10-04T20:00:00Z seven_day_resets=2026-10-06T20:30:00Z", spender.SpendLine(), "the daemon's beat says what the lanes cost and the limit last read")

	// a rejected event is a usage limit until its reset, never a backoff, and the card stays in hand
	program, _ = fakeClaudeStream(t, claudeEvent("rejected", false, 1.0, 0.10, fiveReset, sevenReset), "0.0010", "")
	cl.Program = program
	_, err = cl.RunCard(t.Context(), card("c2"))
	var limited UsageLimited
	require.ErrorAs(t, err, &limited)
	assert.True(t, limited.Until.Equal(fiveReset), "until resetsAt: %s", limited.Until)
	assert.Equal(t, "c2", limited.Session)
	cost, _ = cl.Spent()
	assert.InDelta(t, 0.026, cost, 1e-9, "a limited run is priced too")
	assert.Contains(t, cl.SpendLine(), "runs=3 cost_usd=0.0260 five_hour=1.00", "the limit that stopped the lanes is on the beat's line")
	assert.Empty(t, NewClaude("cy", dir, RealExec, nil).SpendLine(), "no run, nothing to say")
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
// the Claude lanes, the reader row): a read is one trimmed `claude -p` with
// the prompt, on the model of the read's tier placed before --tools (which
// takes every argument after it), priced from its result and its limit read,
// so a claude daemon's reader row needs no script.
func TestAHeadlessClaudeRunsAReadAsOneShotOnItsTiersModelPricedLikeACard(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 18, 30, 0, 0, time.UTC)
	fiveReset, sevenReset := now.Add(90*time.Minute), now.Add(50*time.Hour)
	program, record := fakeClaudeStream(t, claudeEvent("allowed", false, 0.42, 0.10, fiveReset, sevenReset), "0.0300", "")
	cfg := t.TempDir()
	var out strings.Builder
	cl := NewClaude("bob", t.TempDir(), RealExec, &out)
	cl.Program, cl.Now, cl.ConfigDir = program, func() time.Time { return now }, func() string { return cfg }
	var reads ReadHarness = cl

	lt, err := reads.RunRead(t.Context(), ReadModels["heavy"], "read the card")
	require.NoError(t, err)
	assert.Zero(t, lt.Exit)
	_, err = reads.RunRead(t.Context(), "", "read again")
	require.NoError(t, err)

	got := calls(t, record)
	require.Len(t, got, 2)
	assert.Equal(t, cfg+"|-p read the card --output-format stream-json --verbose --model claude-opus-5-5 --strict-mcp-config --disable-slash-commands --no-chrome --tools Bash Read Write Edit Grep Glob", got[0], "the tier's model, before --tools")
	assert.Equal(t, cfg+"|-p read again --output-format stream-json --verbose --strict-mcp-config --disable-slash-commands --no-chrome --tools Bash Read Write Edit Grep Glob", got[1], "no model is the account's own")
	cost, _ := cl.Spent()
	assert.InDelta(t, 0.06, cost, 1e-9, "every read priced from its result")
	assert.Contains(t, out.String(), "claude: run=(read) cost=$0.0300 total=$0.0600 five_hour=0.42")

	program, _ = fakeClaudeStream(t, claudeEvent("rejected", false, 1.0, 0.10, fiveReset, sevenReset), "0.0010", "")
	cl.Program = program
	_, err = cl.RunRead(t.Context(), "", "read at the limit")
	var limited UsageLimited
	require.ErrorAs(t, err, &limited, "a read at the limit is the lanes' pause until the reset")
	assert.True(t, limited.Until.Equal(fiveReset))

	cl.ConfigDir = func() string { return "" }
	_, err = cl.RunRead(t.Context(), "", "no account")
	assert.ErrorContains(t, err, "with no config_dir", "a read runs only as her own account")
}
