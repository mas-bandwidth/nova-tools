package friend

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClaude is a `claude` that prints the stream-json of a headless run: its
// system init, a rate_limit_event per window, the result with its cost. It
// writes the arguments and the stdin it was given beside itself, and answers
// the run named by the text it reads (a card that says "limit" is rejected).
func fakeClaude(t *testing.T, five, seven float64, fiveReset, sevenReset int64) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a shell script stands in for claude")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
echo "$@" >> "` + dir + `/argv"
text=$(cat)
echo "$text" >> "` + dir + `/stdin"
sid=lane-session-1
case "$*" in *--resume*) sid=$(echo "$*" | sed 's/.*--resume \([^ ]*\).*/\1/');; esac
echo '{"type":"system","subtype":"init","session_id":"'$sid'","model":"claude-sonnet-5-5"}'
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}'
status=allowed
case "$text" in *limit*) status=rejected;; esac
echo '{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":` + itoa(fiveReset) + `,"rateLimitType":"five_hour","utilization":` + ftoa(five) + `}}'
echo '{"type":"rate_limit_event","rate_limit_info":{"status":"'$status'","resetsAt":` + itoa(sevenReset) + `,"rateLimitType":"seven_day","utilization":` + ftoa(seven) + `}}'
echo '{"type":"result","subtype":"success","is_error":false,"session_id":"'$sid'","total_cost_usd":0.25,"num_turns":3,"result":"done"}'
`
	path := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

// A headless Claude Code account is a one-shot lane harness, with no shell
// script (docs/SPEC-FRIEND.md, claude one-shot lanes): a session opened by a
// `claude -p` run whose stream-json names it, each card a `--resume` turn
// with the trimmed call, every run priced from its result and its two windows
// read from its rate_limit_events; a rejected window is a rate limit that
// ends at the window's resetsAt, and the lanes wait until then.
func TestAHeadlessClaudeLaneRunsACardPricesItAndReadsItsLimit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	fiveReset, sevenReset := now.Add(3*time.Hour).Unix(), now.Add(48*time.Hour).Unix()
	prog := fakeClaude(t, 0.62, 0.88, fiveReset, sevenReset)
	spend := &Spend{}
	c := &Claude{Dir: t.TempDir(), Run: RealExec, Program: prog, Spend: spend, Now: func() time.Time { return now }}
	var lh LaneHarness = c

	id, err := lh.OpenSession(context.Background(), "You are bud.")
	require.NoError(t, err)
	assert.Equal(t, "lane-session-1", id, "the session the stream's init named")

	lt, err := lh.DeliverTo(context.Background(), id, "one card this turn, card c1")
	require.NoError(t, err)
	assert.Zero(t, lt.Exit)

	argv, err := os.ReadFile(filepath.Join(filepath.Dir(prog), "argv"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(argv)), "\n")
	require.Len(t, lines, 2)
	for _, line := range lines {
		for _, want := range []string{"-p", "--output-format stream-json", "--verbose", "--strict-mcp-config", "--disable-slash-commands", "--no-chrome", "--tools Bash Read Write Edit Grep Glob"} {
			assert.Contains(t, line, want, "the trimmed call")
		}
	}
	assert.NotContains(t, lines[0], "--resume", "the open starts a session")
	assert.Contains(t, lines[1], "--resume lane-session-1")
	stdin, err := os.ReadFile(filepath.Join(filepath.Dir(prog), "stdin"))
	require.NoError(t, err)
	assert.Contains(t, string(stdin), "card c1", "the card goes in on stdin, never in the argument list a variadic --tools would swallow")

	snap := spend.Snapshot()
	assert.Equal(t, 2, snap.Runs)
	assert.InDelta(t, 0.50, snap.CostUSD, 1e-9, "each run priced from its result")
	assert.InDelta(t, 0.62, snap.Usage.FiveHour, 1e-9)
	assert.InDelta(t, 0.88, snap.Usage.SevenDay, 1e-9)
	assert.Equal(t, fiveReset, snap.Usage.FiveHourResets.Unix())
	assert.Equal(t, sevenReset, snap.Usage.SevenDayResets.Unix())
	assert.Contains(t, snap.Line(), "cost_usd=0.5000")
	assert.Contains(t, snap.Line(), "seven_day=0.88")

	lt, err = lh.DeliverTo(context.Background(), id, "the weekly limit card")
	var rate RateLimited
	require.ErrorAs(t, err, &rate, "a rejected window is a rate limit")
	assert.Equal(t, sevenReset, rate.Until.Unix(), "until the spent window's resetsAt")
	assert.InDelta(t, 0.75, spend.Snapshot().CostUSD, 1e-9, "a limited run is priced too")

	var g LaneGovernor
	line := g.WaitUntil(now, rate.Until, rate.Reason)
	assert.Contains(t, line, "lanes wait until")
	assert.True(t, g.Paused(now.Add(24*time.Hour)), "no lane takes a card before the reset")
	assert.False(t, g.Paused(rate.Until), "and they resume at it")
	assert.Equal(t, 4, g.Cap(4), "a window's limit is no reason to narrow the lanes")
}
