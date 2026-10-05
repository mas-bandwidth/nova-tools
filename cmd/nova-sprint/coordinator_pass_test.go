package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A friend's beat carries her session's last pong (friend beat --pong), the server takes it
// as a beat's flag, and the tick's coordinator's pass judges her session deaf from it: the
// judgment is in the coordinator's inbox (docs/SPEC-SPRINT.md section 8, "The coordinator's
// pass").
func TestFriendBeatCarriesTheSessionPongAndTheTickJudgesADeafSession(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend amy", "amy")
	pong := ta.a.now().Add(-11 * time.Minute).UTC().Format(time.RFC3339)
	out := ta.ok("friend beat amy --pong " + pong)
	assert.Contains(t, out, " pong="+pong)
	ta.ok("tick")
	in := ta.ok("inbox")
	assert.Contains(t, in, sprint.NFriendDeaf)
	assert.Contains(t, in, "friend amy")

	code, _, errs := ta.do("friend beat amy --pong yesterday")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--pong wants an RFC3339 time")

	assert.Empty(t, friendBeatReport([]string{"--pong", pong, "--active", pong}), "the server takes both as a beat's flags")
	assert.NotEmpty(t, friendBeatReport([]string{"--pong", "yesterday"}))
}

// set --friend-finish is the friend-finish window the pass judges an idle friend by.
func TestSetFriendFinishIsTheIdleWindow(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend amy", "amy")
	out := ta.ok("set --friend-finish 45m")
	assert.Contains(t, out, "friend-finish 45m")
	code, _, errs := ta.do("set --friend-finish soon")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, errs, "--friend-finish wants a duration")
	assert.Contains(t, ta.ok("set --friend-finish default"), "friend-finish default (30m0s)")
}
