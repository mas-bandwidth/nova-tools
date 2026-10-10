package friend

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A harness's usage-limit refusal sets the friend down until the reset its
// message states, in the zone it names, and she comes back at that time by
// herself; a message with no readable reset holds her with one judgment naming
// the text (usage-limit-reset-read-from-the-message-b.w1; the finding of
// 2026-10-05: "resets Oct 10 at 5am (America/New_York)" read as one hour).
func TestAUsageLimitMessageSetsDownUntilItsStatedReset(t *testing.T) {
	t.Parallel()
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	// 11:42 AM in New York, read on a clock in UTC: the stated zone decides, not the daemon's
	now := time.Date(2026, 10, 5, 11, 42, 0, 0, ny).UTC()

	for _, c := range []struct{ text, want string }{
		{"You've hit your weekly limit · resets Oct 10 at 5am (America/New_York)", "2026-10-10T05:00:00-04:00"},
		{"You've hit your limit · resets 8pm (America/New_York)", "2026-10-05T20:00:00-04:00"},
		{"You've hit your weekly limit · resets Oct 8, 1am (America/New_York)", "2026-10-08T01:00:00-04:00"},
		{"You've hit your limit · resets 9:30am (America/New_York)", "2026-10-06T09:30:00-04:00"}, // passed today: tomorrow
		{"You've hit your weekly limit · resets Oct 1 at 5am (America/New_York)", "2027-10-01T05:00:00-04:00"},
		{"You've hit your limit · resets 5pm (UTC)", "2026-10-05T17:00:00Z"},
		{"Claude AI usage limit reached|1791291600", "2026-10-06T13:00:00Z"},
		{"You've hit your limit · resets Nov 3 at 5am (America/New_York)", "2026-11-03T05:00:00-05:00"}, // after the clocks change
	} {
		hit, ok := ParseLimit("claude", "working on it\n"+c.text+"\n", now, time.Hour)
		require.True(t, ok, c.text)
		want, err := time.Parse(time.RFC3339, c.want)
		require.NoError(t, err)
		assert.True(t, want.Equal(hit.Until), "%s: until %s, want %s", c.text, hit.Until.Format(time.RFC3339), c.want)
		assert.True(t, hit.Named, c.text)
		lim, found := ReadLimit(c.text+"\n", now)
		assert.True(t, found && want.Equal(lim.Until), "%s: ReadLimit reads it alike: %s", c.text, lim.Until.Format(time.RFC3339))
	}
	for _, text := range []string{
		"You've hit your weekly limit · resets Oct 10 at 5am (Mars/Olympus_Mons)", // a zone that does not load is no guess
		"You've hit your weekly limit · resets Feb 30 at 5am (America/New_York)",
		"You've hit your weekly limit · resets soon",
	} {
		hit, ok := ParseLimit("claude", text, now, time.Hour)
		require.True(t, ok, text)
		assert.False(t, hit.Named, "%s names no reset this reads", text)
	}

	// the friend: down until the stated reset, held, then back by herself when it passes
	coord := bustest.NewFake(now, "coord", "bob")
	clock := &limitClock{t: now}
	var downs, judged, ups []string
	l := &Limits{Now: clock.now, Harness: "claude", Rest: time.Hour, Nonce: func() string { return "w4k3up" },
		Down: func(until time.Time, reason string) { downs = append(downs, until.UTC().Format(time.RFC3339)) },
		Up:   func(string) { ups = append(ups, "up") },
		Unread: func(text string) {
			judged = append(judged, text)
			subject, body := LimitUnreadText("bob", time.Hour, text)
			_, err := (&bus.Bus{Store: coord}).Send(context.Background(), bus.Message{From: "bob", To: []string{"coord"}, Subject: subject, Body: body})
			require.NoError(t, err)
		},
	}
	se := &scriptExec{
		outs:  []string{"You've hit your weekly limit · resets Oct 10 at 5am (America/New_York)\n", "w4k3up\n", "done\n"},
		exits: []int{1, 0, 0},
	}
	d := l.Gate(&OpenCode{Dir: "/w/bob", Session: "s1", Run: l.Watch(se.run)})
	_, err = d.Deliver(context.Background(), "card")
	var deferred Deferred
	require.ErrorAs(t, err, &deferred)
	require.Equal(t, []string{time.Date(2026, 10, 10, 5, 0, 0, 0, ny).UTC().Format(time.RFC3339)}, downs, "down until the stated reset, not an hour")
	assert.Empty(t, judged, "a readable reset is no judgment")

	clock.t = time.Date(2026, 10, 10, 4, 59, 59, 0, ny).UTC()
	_, err = d.Deliver(context.Background(), "card")
	require.ErrorAs(t, err, &deferred, "held until the reset")
	assert.Len(t, se.texts, 1, "nothing runs into the same wall")

	clock.t = time.Date(2026, 10, 10, 5, 0, 1, 0, ny).UTC()
	_, err = d.Deliver(context.Background(), "card")
	require.NoError(t, err, "back at the reset by herself: the wake, then the card")
	assert.Equal(t, []string{"up"}, ups)
	require.Len(t, se.texts, 3)
	assert.Equal(t, "card", se.texts[2])

	// a message with no readable reset: held for the rest, one judgment naming the text,
	// not said again while the wakes keep meeting the same wall
	unread := "You've hit your weekly limit · resets whenever\n"
	se2 := &scriptExec{outs: []string{unread, unread, unread, "w4k3up\n", "done\n"}, exits: []int{1, 1, 1, 0, 0}}
	d = l.Gate(&OpenCode{Dir: "/w/bob", Session: "s1", Run: l.Watch(se2.run)})
	start := clock.t
	_, err = d.Deliver(context.Background(), "card")
	require.ErrorAs(t, err, &deferred)
	until, _, limited := l.Limited()
	require.True(t, limited, "held")
	assert.True(t, start.Add(time.Hour).Equal(until))
	for i := 1; i <= 2; i++ {
		clock.t = start.Add(time.Duration(i)*time.Hour + time.Second)
		_, err = d.Deliver(context.Background(), "card")
		require.ErrorAs(t, err, &deferred, "the wake meets the same wall")
	}
	require.Equal(t, []string{"You've hit your weekly limit · resets whenever"}, judged, "one judgment, naming the text")
	pending, fresh, err := (&bus.Bus{Store: coord}).Peek(context.Background(), "coord")
	require.NoError(t, err)
	waiting := append(pending, fresh...)
	require.Len(t, waiting, 1, "the coordinator holds one judgment")
	assert.Contains(t, waiting[0].Message().Body, "resets whenever")
	assert.Contains(t, waiting[0].Message().Body, "nova-sprint friend down bob")

	clock.t = start.Add(3*time.Hour + time.Second)
	_, err = d.Deliver(context.Background(), "card")
	require.NoError(t, err, "a wake answered ends the hold")
	_, _, limited = l.Limited()
	assert.False(t, limited)

	se3 := &scriptExec{outs: []string{unread}, exits: []int{1}}
	d = l.Gate(&OpenCode{Dir: "/w/bob", Session: "s1", Run: l.Watch(se3.run)})
	_, err = d.Deliver(context.Background(), "card")
	require.ErrorAs(t, err, &deferred)
	assert.Len(t, judged, 2, "a new hold after she was up is a new judgment")
}
