package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A friend's beat carries her session's answer to a check her daemon asked (friend beat
// --check, then --pong naming it), the server takes them as a beat's flags, and the tick's
// coordinator's pass judges her session deaf from the last proof: the judgment is in the
// coordinator's inbox (docs/SPEC-SPRINT.md section 8, "The coordinator's pass").
func TestFriendBeatCarriesTheSessionPongAndTheTickJudgesADeafSession(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend amy", "amy")
	back := sprint.FriendDeafAfter + time.Minute
	ta.step(-back)
	ta.ok("friend beat amy --check n1 --run r1")
	out := ta.ok("friend beat amy --pong n1 --run r1")
	assert.Contains(t, out, " proved=n1 pong=")
	ta.step(back)
	ta.ok("friend beat amy")
	ta.ok("tick")
	in := ta.ok("inbox")
	assert.Contains(t, in, sprint.NFriendDeaf)
	assert.Contains(t, in, "friend amy")

	code, _, errs := ta.do("friend beat amy --check a:b")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--check wants a nonce")

	assert.Empty(t, friendBeatReport([]string{"--check", "n2", "--run", "r1", "--pong", "n1", "--active", "2030-01-02T03:04:05Z"}), "the server takes them as a beat's flags")
	assert.NotEmpty(t, friendBeatReport([]string{"--check", "a b"}))
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
