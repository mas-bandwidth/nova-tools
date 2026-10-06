package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stats tidy end to end on the twin (internal/sprint's TestStatsTidyZeroesCountersAndKeepsTheWork
// drives the cards): it names what it tidies and why, a dry run writes nothing, a second tidy
// within a minute is refused, exit 1, and stats counts from the tidy.
func TestStatsTidyVerbRefusesASecondWithinAMinute(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")

	code, _, errs := ta.do("stats tidy --reason r")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "names nothing to tidy")
	code, _, errs = ta.do("stats tidy --all")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "wants --reason <text>")

	out := ta.ok("stats tidy --all --reason 'a fresh start' --dry-run")
	assert.Contains(t, out, "STATS-TIDY DRY-RUN would move since=")
	assert.NotContains(t, ta.ok("stats"), "since=", "a dry run records nothing")

	out = ta.ok("stats tidy --fleet --friends --reason 'a fresh start'")
	assert.Contains(t, out, "STATS-TIDY OK since=")
	assert.Contains(t, out, "kinds=friends,fleet moved=0 kept=0 archive=stats:archive:")
	assert.Contains(t, ta.ok("stats"), " since=")

	ta.mu.Lock()
	ta.now = ta.now.Add(30 * time.Second)
	ta.mu.Unlock()
	code, _, errs = ta.do("stats tidy --all --reason again")
	require.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "nova-sprint stats tidy REFUSED: the last tidy was at ")
	assert.Contains(t, errs, "a second within 1m0s is refused, nothing written")

	// stats --routes takes its window from the last tidy of the routes; with none it wants --since
	code, _, errs = ta.do("stats --routes")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--routes wants --since")
	ta.mu.Lock()
	ta.now = ta.now.Add(time.Minute)
	ta.mu.Unlock()
	ta.ok("stats tidy --routes --reason routes")
	ta.ok("stats --routes")
}
