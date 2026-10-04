package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// log --json --since with a window wider than 22h returns entries since that time.
func TestLogJsonSinceWithWindowWiderThan22HoursReturnsSinceTime(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.a.loc = time.UTC
	ta.ok("init --readers reader-a --members m1")

	ta.ok("add --stream s1 --count 1 --brief-file " + writeBrief(t, "early card"))
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --head h1 --report 'done'")

	ta.mu.Lock()
	ta.now = ta.now.Add(24 * time.Hour)
	ta.mu.Unlock()

	ta.ok("add --stream s1 --count 1 --brief-file " + writeBrief(t, "later card"))
	ta.deal(1)
	ta.ok("take --as m1 s1-2.w1@1")
	ta.ok("finish --as m1 s1-2.w1@1 --head h2 --report 'done'")

	out := ta.ok("log --since 24h")
	assert.Contains(t, out, "s1-1")
	assert.Contains(t, out, "s1-2")
	assert.NotContains(t, out, "LOG OK lines=0")

	out = ta.ok("log --since 25h")
	assert.Contains(t, out, "s1-1")
	assert.Contains(t, out, "s1-2")
	assert.NotContains(t, out, "LOG OK lines=0")

	out = ta.ok("log --since 30h")
	assert.Contains(t, out, "s1-1")
	assert.Contains(t, out, "s1-2")
	assert.NotContains(t, out, "LOG OK lines=0")
}

// log --json --since with an RFC3339 time wider than 22h back returns entries.
func TestLogJsonSinceWithRFC3339TimeWiderThan22Hours(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.a.loc = time.UTC
	ta.ok("init --readers reader-a --members m1")

	ta.ok("add --stream s1 --count 1 --brief-file " + writeBrief(t, "early card"))
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --head h1 --report 'done'")

	// Get the current time and move 24 hours forward
	ta.mu.Lock()
	startTime := ta.now
	ta.now = ta.now.Add(24 * time.Hour)
	ta.mu.Unlock()

	ta.ok("add --stream s1 --count 1 --brief-file " + writeBrief(t, "later card"))
	ta.deal(1)
	ta.ok("take --as m1 s1-2.w1@1")
	ta.ok("finish --as m1 s1-2.w1@1 --head h2 --report 'done'")

	// Query using RFC3339 time - asking for entries at or after startTime
	// This is 24h in the past, which is wider than 22h
	out := ta.ok("log --since " + startTime.Format(time.RFC3339))
	assert.Contains(t, out, "s1-1")
	assert.Contains(t, out, "s1-2")
	assert.NotContains(t, out, "LOG OK lines=0")

	// Query asking for entries at or after 1 hour before startTime
	// This is 25h in the past, much wider than 22h
	out = ta.ok("log --since " + startTime.Add(-time.Hour).Format(time.RFC3339))
	assert.Contains(t, out, "s1-1")
	assert.Contains(t, out, "s1-2")
	assert.NotContains(t, out, "LOG OK lines=0")
}

// log --json --since returns every entry since that time with both duration
// and RFC3339 formats, including windows wider than 22 hours.
func TestLogJsonSinceReturnsEntriesWithin22HourWindow(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.a.loc = time.UTC
	ta.ok("init --readers reader-a --members m1")

	// Add card 1 at T+0 (start)
	ta.ok("add --stream s1 --count 1 --brief-file " + writeBrief(t, "early"))
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --head h1 --report 'done'")

	// Advance time and add card 2
	ta.mu.Lock()
	startTime := ta.now
	ta.now = ta.now.Add(24 * time.Hour)
	ta.mu.Unlock()

	ta.ok("add --stream s1 --count 1 --brief-file " + writeBrief(t, "later"))
	ta.deal(1)
	ta.ok("take --as m1 s1-2.w1@1")
	ta.ok("finish --as m1 s1-2.w1@1 --head h2 --report 'done'")

	// At T+24h, --since 25h should return both cards
	out := ta.ok("log --since 25h")
	assert.Contains(t, out, "s1-1")
	assert.Contains(t, out, "s1-2")

	// Query using RFC3339 time - ask for entries at or after startTime
	// This is 24h in the past (wider than 22h)
	out = ta.ok("log --since " + startTime.Format(time.RFC3339))
	assert.Contains(t, out, "s1-1")
	assert.Contains(t, out, "s1-2")
}
