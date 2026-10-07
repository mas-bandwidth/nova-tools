package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/sprint"
)

// A friend whose harness is at its usage limit beats down with the until and the reason
// (friend beat --until --reason, limits-mean-down-w-r5.w1~15): her row is down however
// fresh the beat and whatever her session's evidence, why says until when and why, her
// report carries the pair, and a beat without --until withdraws the word, so the wake
// ping her session answered has her up again (a beat alone never does:
// presence-from-session-only).
func TestFriendBeatDownUntilCarriesTheReasonAndTheUntil(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync")
	ta.ok("friend beat amy")
	require.Equal(t, sprint.Down, whereFriends(ta)["amy"].Status, "a beat is no evidence")
	ta.pong("amy")
	require.Equal(t, sprint.Up, whereFriends(ta)["amy"].Status)

	until := ta.a.now().Add(time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	out := ta.ok("friend beat amy --until " + until + " --reason limit")
	assert.Contains(t, out, " down=true until="+until)
	f := whereFriends(ta)["amy"]
	assert.Equal(t, sprint.Down, f.Status, "a fresh beat that says down is down, over her session's pong")
	require.NotNil(t, f.Report)
	assert.Equal(t, until, f.Report.Until.Format(time.RFC3339))
	assert.Equal(t, "limit", f.Report.Reason)
	presence := sprint.FriendPresence{Beat: sprint.Beat{At: ta.a.now(), Friend: f.Report}}
	assert.Equal(t, "her beat says down until "+until+": limit", sprint.FriendDownWhy(presence, ta.a.now()))

	ta.ok("friend beat amy")
	assert.Equal(t, sprint.Up, whereFriends(ta)["amy"].Status, "a beat without --until withdraws the word: her session's pong has her up again")

	code, _, errs := ta.do("friend beat amy --until soon")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--until wants an RFC3339 time")
	code, _, errs = ta.do("friend beat amy --reason limit")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--reason says why she is down, and wants --until")
}
