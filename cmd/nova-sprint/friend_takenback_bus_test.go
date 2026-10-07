package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A card taken back out of a lane the friend was working (a hold's take-back, a blocker's
// eviction) reaches her session: friend sync, seeing her queue file say working and the card
// withdrawn on her row, tells her on the bus once, why and which generation's branch keeps her
// push (the cold read of nova-tools#5405, item 4); the next sync says nothing more.
func TestAFriendIsToldOnTheBusWhenHerWorkingCardIsTakenBack(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	ta.ok("friend sync --root " + root)
	ta.startFriend("amy", 1)
	ta.ok("friend sync --root " + root) // her queue file says working
	sent := func() []bus.Message {
		ta.mu.Lock()
		defer ta.mu.Unlock()
		var out []bus.Message
		for _, m := range ta.sent {
			if strings.Contains(m.Subject, "taken back") {
				out = append(out, m)
			}
		}
		return out
	}
	require.Empty(t, sent())
	ta.ok("hold amy --reason 'the base is red' --return")
	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD TAKEN-BACK friend=amy card=s1-1.w1 gen=1: taken back by the hold of friend amy; told on the bus")
	msgs := sent()
	require.Len(t, msgs, 1)
	assert.Equal(t, []string{"amy"}, msgs[0].To)
	assert.Equal(t, "card s1-1.w1 taken back: taken back by the hold of friend amy", msgs[0].Subject)
	assert.Contains(t, msgs[0].Body, "Stop work on your sprint card s1-1.w1 (job s1-1.w1, generation 1)")
	assert.Contains(t, msgs[0].Body, "a finish of generation 1 is refused as stale")
	assert.Contains(t, msgs[0].Body, "Its branch sprint/s1-1.w1.g1.e0 keeps what you pushed")
	out = ta.ok("friend sync --root " + root)
	assert.NotContains(t, out, "TAKEN-BACK", "told once: her queue file says taken now")
	assert.Len(t, sent(), 1)
}
