package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The remind verb on the twin with the fake clock (docs/SPEC-SPRINT.md,
// "Timers"): set by --in and --at, listed, missed after a gap, seen by --ack.

func TestRemindParsesAtAsRFC3339OrALocalClockTime(t *testing.T) {
	t.Parallel()
	zone, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	now := time.Date(2026, 10, 4, 18, 30, 0, 0, zone)
	for _, c := range []struct{ in, want string }{
		{"7:30pm", "2026-10-04T19:30:00-04:00"},
		{"7:30 PM", "2026-10-04T19:30:00-04:00"},
		{"7pm", "2026-10-04T19:00:00-04:00"},
		{"19:30", "2026-10-04T19:30:00-04:00"},
		{"6:30pm", "2026-10-05T18:30:00-04:00"}, // now: the next one, tomorrow
		{"9am", "2026-10-05T09:00:00-04:00"},
		{"2026-10-04T23:00:00Z", "2026-10-04T19:00:00-04:00"},
	} {
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			got, err := parseAt(c.in, now, zone)
			require.NoError(t, err)
			want, _ := time.Parse(time.RFC3339, c.want) // ignored: the table's times parse
			assert.True(t, got.Equal(want), "%s: %s, want %s", c.in, got, want)
		})
	}
	_, err = parseAt("half past seven", now, zone)
	assert.ErrorContains(t, err, "no clock time")
}

func TestRemindSetListMissedAfterAGapAndAck(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	out := ta.ok("remind --in 1h --note 'one hour' --dry-run")
	assert.Contains(t, out, "REMIND OK id=- for=coordinator")
	assert.Contains(t, out, "nothing written")
	assert.Contains(t, ta.ok("remind --list"), "REMIND OK timers=0", "a dry run writes nothing")
	out = ta.ok("remind --in 1h --note 'one hour'")
	assert.Contains(t, out, "REMIND OK id=t1 for=coordinator due="+t0.Add(time.Hour).Format(time.RFC3339)+" in=1h0m0s within=1h0m0s")
	ta.a.loc = time.UTC
	out = ta.ok("remind --at 3:30am --note 'at a clock time' --within 2h")
	assert.Contains(t, out, "id=t2 for=coordinator due=2030-01-02T03:30:00Z", "the next 3:30am in the zone")
	ta.a.sleep(time.Hour) // the session quits; the server ticks on
	ta.ok("tick")
	assert.Contains(t, ta.ok("remind --list"), "TIMER t1 state=fired")
	ta.a.sleep(time.Hour) // two hours after it was set, nobody has seen it
	ta.ok("tick")
	out = ta.ok("remind --list --missed")
	assert.Contains(t, out, "TIMER t1 state=fired")
	assert.Contains(t, out, "MISSED unseen (60m ago)")
	assert.Contains(t, out, "TIMER t2 state=fired", "fired at 3:30 and unseen: missed too")
	view := ta.ok("view coordinator")
	assert.Contains(t, view, "T 0 t:t1", "the view: %s", view)
	lines := strings.Split(view, "\n")
	require.GreaterOrEqual(t, len(lines), 2, "the view: %s", view)
	assert.Contains(t, lines[1], "timer t1 (one hour) fired 60m ago, unseen", "the missed timer leads: %s", view)
	out = ta.ok("remind --ack t1")
	assert.Contains(t, out, "REMIND OK acked=t1 state=seen changed=true")
	assert.NotContains(t, ta.ok("remind --list --missed"), "TIMER t1", "seen: off the missed list")
	assert.Contains(t, ta.ok("remind --list"), "TIMER t1 state=seen")
}

func TestRemindRefusals(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	for _, c := range []struct {
		line string
		code int
		says string
	}{
		{"remind", 2, "give one of"},
		{"remind --in 1h", 2, "--note <text>"},
		{"remind --in 1h --at 7pm --note x", 2, "one due time"},
		{"remind --at soon --note x", 2, "no clock time"},
		{"remind --list --cancel t1", 2, "give one of"},
		{"remind --in 1h --note x --for nobody", 1, "no friend of the sprint"},
		{"remind --cancel t9", 1, "no timer t9"},
		{"remind --ack t9", 1, "no timer t9"},
	} {
		code, _, errs := ta.do(c.line)
		assert.Equal(t, c.code, code, "%s: %s", c.line, errs)
		assert.Contains(t, errs, c.says, c.line)
		assert.Contains(t, errs, "; run: ", c.line)
	}
}
