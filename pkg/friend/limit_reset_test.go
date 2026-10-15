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
	l := &Limits{Now: clock.now, Harness: "claude", Rest: time.Hour,
		Down: func(until time.Time, reason string) { downs = append(downs, until.UTC().Format(time.RFC3339)) },
		Up:   func() { ups = append(ups, "up") },
		Unread: func(text string) {
			judged = append(judged, text)
			subject, body := LimitUnreadText("bob", DefaultPronouns, time.Hour, text)
			_, err := (&bus.Bus{Store: coord}).Send(context.Background(), bus.Message{From: "bob", To: []string{"coord"}, Subject: subject, Body: body})
			require.NoError(t, err)
		},
	}
	se := &scriptExec{
		outs:  []string{"You've hit your weekly limit · resets Oct 10 at 5am (America/New_York)\n", "done\n"},
		exits: []int{1, 0},
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
	require.NoError(t, err, "back at the reset by herself: the card goes in, no wake turn")
	assert.Equal(t, []string{"up"}, ups)
	require.Len(t, se.texts, 2)
	assert.Equal(t, "card", se.texts[1])

	// a message with no readable reset: held for the rest, one judgment naming the text,
	// not said again while the turns after each rest keep meeting the same wall
	unread := "You've hit your weekly limit · resets whenever\n"
	se2 := &scriptExec{outs: []string{unread, unread, unread, "done\n"}, exits: []int{1, 1, 1, 0}}
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
		require.ErrorAs(t, err, &deferred, "the turn after the rest meets the same wall")
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
	require.NoError(t, err, "a turn that runs clean ends the hold")
	_, _, limited = l.Limited()
	assert.False(t, limited)

	se3 := &scriptExec{outs: []string{unread}, exits: []int{1}}
	d = l.Gate(&OpenCode{Dir: "/w/bob", Session: "s1", Run: l.Watch(se3.run)})
	_, err = d.Deliver(context.Background(), "card")
	require.ErrorAs(t, err, &deferred)
	assert.Len(t, judged, 2, "a new hold after she was up is a new judgment")
}

// A limit's reset diagnostic belongs to the same episode notice. A funds
// refusal delegates to the spend cap, which emits its own single judgment.
func TestALimitEpisodeEmitsOneComposedNotice(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, text string
		funds      bool
		unread     bool
	}{
		{"known reset", "You've hit your weekly limit · resets Oct 10 at 5am (America/New_York)", false, false},
		{"unknown reset", "You've hit your weekly limit · resets whenever", false, true},
		{"funds", "Credit balance is too low", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			notices, funds := 0, 0
			now := t0
			l := &Limits{Now: func() time.Time { return now }, Harness: "claude"}
			l.Notice = func(_ time.Time, _ string, unread bool) { notices++; assert.Equal(t, tc.unread, unread) }
			l.Funds = func(string) { funds++ }
			l.see(tc.text, true)
			now = now.Add(time.Second)
			l.see(tc.text, true)
			assert.Equal(t, 1, notices+funds, "one message for the episode, even with unread reset evidence")
			assert.Equal(t, tc.funds, funds == 1)
			now = l.until.Add(time.Second)
			l.Limited()
			l.see("done", false)
			l.see(tc.text, true)
			assert.Equal(t, 2, notices+funds, "a later hold after recovery starts a new episode")
		})
	}
}

// In-flight lanes can learn a later reset during a hold. State and down beats
// extend immediately, while the coordinator still receives one episode notice.
func TestLimitHoldExtendsWithoutASecondNotice(t *testing.T) {
	t.Parallel()
	now := t0
	notices, downs := 0, 0
	l := &Limits{Now: func() time.Time { return now }, Harness: "claude",
		Notice: func(time.Time, string, bool) { notices++ },
		Down:   func(time.Time, string) { downs++ },
	}
	l.see("You've hit your usage limit. Try again in 1 hour.", true)
	first, _, held := l.Limited()
	require.True(t, held)
	now = now.Add(time.Second)
	l.see("You've hit your usage limit. Try again in 3 hours.", true)
	later, _, held := l.Limited()
	assert.True(t, held)
	assert.True(t, later.After(first))
	assert.Equal(t, 1, notices)
	assert.Equal(t, 2, downs, "the down state refreshes even though the notice is deduplicated")
}

func TestLimitFundsEscalationStillHaltsAndNotifiesOnce(t *testing.T) {
	t.Parallel()
	for _, initial := range []string{
		"You've hit your usage limit. Try again in 1 hour.",
		"Insufficient AI Credits. Your credits will refresh in 10 minutes.",
	} {
		t.Run(initial, func(t *testing.T) {
			t.Parallel()
			now := t0
			notices, funds := 0, 0
			l := &Limits{Now: func() time.Time { return now }, Harness: "claude",
				Notice: func(time.Time, string, bool) { notices++ },
				Funds:  func(string) { funds++ },
			}
			l.see(initial, true)
			require.Equal(t, 1, notices)
			for range 2 {
				now = now.Add(time.Second)
				l.see("Credit balance is too low", true)
			}
			assert.Equal(t, 1, notices)
			assert.Equal(t, 1, funds, "funds hold callback both halts spend and emits its distinct judgment")
			assert.Equal(t, KindCredits, l.Kind())
			l.see("You've hit your usage limit. Try again in 1 hour.", true)
			assert.Equal(t, KindCredits, l.Kind(), "an in-flight timed refusal cannot downgrade credit exhaustion")
		})
	}
}
