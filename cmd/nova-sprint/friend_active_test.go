package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// beatActive is friend beat with the session's last activity ago before the app's clock.
func beatActive(ta *testApp, friend string, ago time.Duration) string {
	return "friend beat " + friend + " --active " + ta.a.now().Add(-ago).UTC().Format(time.RFC3339)
}

// A friend's beat carries the newest write her daemon found, her friends row keeps it, and
// the table shows how long ago it was; a beat that carries none shows "-".
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
	assert.True(t, whereFriends(ta)["amy"].Active.IsZero(), "a beat with none keeps none")
	assert.NotContains(t, ta.ok("where --all"), "m ago")

	code, _, errs := ta.do("friend beat amy --active yesterday")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--active wants an RFC3339 time")
}

// Her daemon answers and her session writes nothing: with cards dealt to her, past the
// setting (20m by default) it is an alarm; fresh activity, a longer setting or no cards
// is none.
func TestAFriendHoldingCardsWhoseSessionWritesNothingIsAnAlarm(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	ta.ok(beatActive(ta, "amy", 5*time.Minute))
	_, ok := item(ta.coordView(""), "f:amy")
	assert.False(t, ok, "she wrote five minutes ago")

	ta.ok(beatActive(ta, "amy", sprint.FriendIdleDefault+time.Minute))
	f, ok := item(ta.coordView(""), "f:amy")
	require.True(t, ok, "21m of silence while a card is dealt to her")
	assert.Equal(t, itemFriend, f.T)
	assert.Equal(t, "friend idle", f.W)
	assert.Contains(t, f.S, "a daemon that answers and a session that has written nothing for 21m")
	assert.Contains(t, f.S, "holds 1 ready, 0 working")

	// a longer setting takes it off, default puts it back
	ta.ok("set --friend-idle 1h")
	ta.ok("tick")
	it, ok := item(ta.coordView(""), "f:amy")
	assert.False(t, ok, "21m is inside an hour: %+v", it)
	ta.ok("set --friend-idle default")
	ta.ok("tick")
	_, ok = item(ta.coordView(""), "f:amy")
	assert.True(t, ok)

	// a beat that reports no activity is no evidence of idleness: the stale-report rule stands
	ta.ok("friend beat amy")
	_, ok = item(ta.coordView(""), "f:amy")
	assert.False(t, ok)

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
