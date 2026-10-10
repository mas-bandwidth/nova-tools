package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stats reset end to end on the twin (internal/sprint's
// TestAStatsResetCountsEveryFigureFromItsMarkAndMovesNothing drives priced cards): it wants
// a reason, a dry run writes nothing, --show prints the mark in force, stats and where
// --json count from it, and a second reset replaces it.
func TestStatsResetVerbMarksShowsAndReplaces(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")

	code, _, errs := ta.do("stats reset")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "wants --reason <text>")
	code, _, errs = ta.do("stats reset --show --reason r")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--show only reads the mark")

	assert.Contains(t, ta.ok("stats reset --show"), "STATS-RESET none")
	out := ta.ok("stats reset --reason 'count from now' --dry-run")
	assert.Contains(t, out, "STATS-RESET DRY-RUN would mark at=")
	assert.Contains(t, out, "ROW m1 done=0 ok=0 failed=0")
	assert.Contains(t, ta.ok("stats reset --show"), "STATS-RESET none", "a dry run writes no mark")
	assert.NotContains(t, ta.ok("stats"), "since=", "a dry run records nothing")

	out = ta.ok("stats reset --reason 'count from now'")
	assert.Contains(t, out, "STATS-RESET OK at=")
	assert.Contains(t, out, "reason=count from now")
	assert.NotContains(t, out, "replaced=")
	stats := ta.ok("stats")
	assert.Contains(t, stats, "(stats reset)")
	assert.Contains(t, stats, " since=")

	var v struct {
		StatsReset *struct {
			At     time.Time `json:"at"`
			Reason string    `json:"reason"`
			Landed int64     `json:"landed"`
		} `json:"stats_reset"`
		Tables map[string]map[string]map[string]any `json:"tables"`
	}
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json")), &v))
	require.NotNil(t, v.StatsReset, "where --json carries the mark")
	assert.Equal(t, "count from now", v.StatsReset.Reason)
	assert.Zero(t, v.StatsReset.Landed, "nothing landed since: the page's per card is blank")
	assert.Equal(t, "0", v.Tables["fleet"]["m1"]["done"])

	// a second reset replaces the first
	ta.mu.Lock()
	ta.now = ta.now.Add(time.Second)
	ta.mu.Unlock()
	out = ta.ok("stats reset --reason again")
	assert.Contains(t, out, "replaced=")
	show := ta.ok("stats reset --show")
	assert.Contains(t, show, "STATS-RESET mark at=")
	assert.Contains(t, show, "reason=again")
	assert.False(t, strings.Contains(show, "count from now"), "the first mark is replaced")

	// under --op, a retry returns the mark it wrote and writes nothing
	ta.mu.Lock()
	ta.now = ta.now.Add(time.Second)
	ta.mu.Unlock()
	out = ta.ok("stats reset --reason third --op reset-3")
	assert.Contains(t, out, "STATS-RESET OK at=")
	ta.mu.Lock()
	ta.now = ta.now.Add(time.Second)
	ta.mu.Unlock()
	again := ta.ok("stats reset --reason third --op reset-3")
	assert.Contains(t, again, "STATS-RESET REPLAY op=reset-3 nothing written")
	at := func(line string) string {
		_, rest, _ := strings.Cut(line, " at=")
		f, _, _ := strings.Cut(rest, " ")
		return f
	}
	assert.Equal(t, at(out), at(again), "the same mark")
	assert.NotContains(t, again, "replaced=")
}
