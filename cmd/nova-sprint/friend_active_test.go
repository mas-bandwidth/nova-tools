package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// beatActive is friend beat with the session's last activity ago before the app's clock.
func beatActive(ta *testApp, friend string, ago time.Duration) string {
	return "friend beat " + friend + " --active " + ta.a.now().Add(-ago).UTC().Format(time.RFC3339)
}

// A friend's beat carries the newest write her daemon found, her friends row keeps it, and
// the table shows how long ago it was; a beat that carries none preserves the last known activity.
func TestFriendBeatCarriesTheLastSessionActivityOntoHerRowAndTheTable(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync")
	out := ta.ok(beatActive(ta, "amy", 30*time.Minute))
	assert.Contains(t, out, " active=")
	f := whereFriends(ta)["amy"]
	require.False(t, f.Active.IsZero())
	assert.Equal(t, 30*time.Minute, ta.a.now().Sub(f.Active).Truncate(time.Second))
	assert.Contains(t, ta.ok("where --all"), "30m ago")

	ta.ok("friend beat amy")
	assert.Equal(t, f.Active, whereFriends(ta)["amy"].Active, "a liveness beat preserves known activity")
	assert.Contains(t, ta.ok("where --all"), "30m ago")

	code, _, errs := ta.do("friend beat amy --active yesterday")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--active wants an RFC3339 time")
}

// Her daemon answers and nothing of hers moves (no session write, session proof or answer,
// finish, report or card move): with cards dealt to her, past the setting (20m by default)
// it is an alarm; fresh evidence of any kind, a longer setting or no cards is none. A stale
// walk of her directory alone is never the alarm (2026-10-06: it read 3d while she
// reported hourly).
func TestAFriendHoldingCardsWhoseSessionWritesNothingIsAnAlarm(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	ta.startFriend("amy", 1)
	ta.later(25 * time.Minute) // her start is a card move of hers: evidence of work until it ages
	ta.beatUp("amy")           // her session answers the wake ping now: she is up
	ta.ok(beatActive(ta, "amy", 5*time.Minute))
	_, ok := item(ta.coordView(""), "f:amy")
	assert.False(t, ok, "she wrote five minutes ago")

	ta.ok(beatActive(ta, "amy", 3*24*time.Hour))
	_, ok = item(ta.coordView(""), "f:amy")
	assert.False(t, ok, "her walk reads 3d, and her session answered a moment ago: no alarm")

	// past a shorter setting with no evidence since her answer, it is the alarm
	ta.ok("set --friend-idle 5m")
	ta.later(6 * time.Minute)
	ta.ok(beatActive(ta, "amy", 3*24*time.Hour))
	ta.ok("tick")
	f, ok := item(ta.coordView(""), "f:amy")
	require.True(t, ok, "6m with no evidence while a card is dealt to her")
	assert.Equal(t, itemFriend, f.T)
	assert.Equal(t, "friend idle", f.W)
	assert.Contains(t, f.S, "a daemon that answers and no evidence of work for 6m")
	assert.Contains(t, f.S, "holds 0 ready, 1 working")

	// a longer setting takes it off, the shorter puts it back
	ta.ok("set --friend-idle 1h")
	ta.ok("tick")
	it, ok := item(ta.coordView(""), "f:amy")
	assert.False(t, ok, "6m is inside an hour: %+v", it)
	ta.ok("set --friend-idle 5m")
	ta.ok("tick")
	_, ok = item(ta.coordView(""), "f:amy")
	assert.True(t, ok)

	// A liveness beat preserves the stale report; it cannot clear an existing idle alarm.
	ta.ok("friend beat amy")
	_, ok = item(ta.coordView(""), "f:amy")
	assert.True(t, ok)

	code, _, errs := ta.do("set --friend-idle soon")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "--friend-idle wants a duration above zero")
}

func TestAnIdleFriendWithNoCardsIsNoAlarm(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync")
	ta.ok(beatActive(ta, "amy", 3*time.Hour))
	_, ok := item(ta.coordView(""), "f:amy")
	assert.False(t, ok, "nothing is dealt to her")
}
