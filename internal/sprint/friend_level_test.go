package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
)

// friend level evens the ready queues of the friends of a class, as fleet level evens the
// members': only a card for any friend, ready, and not started moves.
func TestFriendLevelEvensTheReadyQueuesOfAClass(t *testing.T) {
	t.Parallel()
	briefs := []string{friendBrief("friend amy")}
	for range 6 {
		briefs = append(briefs, friendBrief("friend"))
	}
	w := friendWorld(t, briefs...)
	// amy alone up at width 2: her room of 4 is filled, s1-1 (hers by name) and s1-2 working
	dealStarted(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash"})
	amy, bob, cat := FriendRow("amy"), FriendRow("bob"), FriendRow("cat")
	require.Equal(t, 2, w.s.Fleet.Count(amy, Ready))
	seats := []FriendSeat{{Name: "amy", Width: 2, Status: Up, Class: "flash"}, {Name: "bob", Width: 2, Status: Up, Class: "flash"}, {Name: "cat", Width: 2, Status: Up, Class: "pro"}}

	// s1-4 is started (her beat names it running): it stays
	p := w.must(FriendLevel(w.s, FriendLevelReq{Seats: seats, Started: map[string]string{"s1-4.w1": "her beat names it running"}}))
	require.Len(t, p.Units, 1, "amy's backlog 2 against bob's -2: one card moves; 1 against -1 would move s1-4, but she started it")
	assert.Contains(t, p.Units[0].Moved, "s1-3.w1 friend.amy:ready -> friend.bob:ready gen=2; moved=1 to bob(1) from amy(1)")
	wc := w.s.Fleet.Card("s1-3.w1")
	assert.Equal(t, bob, wc.Row)
	assert.Equal(t, Ready, wc.Col, "bob had a lane free, and the card is ready on his row until he starts it")
	assert.Empty(t, wc.F("taken"))
	assert.Equal(t, Ready, w.s.Fleet.Card("s1-4.w1").Col)
	assert.Equal(t, amy, w.s.Fleet.Card("s1-4.w1").Row, "the started one stays")
	assert.Equal(t, 0, w.s.Fleet.Count(cat, Working)+w.s.Fleet.Count(cat, Ready), "cat is of another class")
	assert.Empty(t, Check(w.s, nil))

	// not started now, s1-4 moves too (1 against -1); then they are even and nothing moves
	w.must(FriendLevel(w.s, FriendLevelReq{Seats: seats}))
	assert.Equal(t, bob, w.s.Fleet.Card("s1-4.w1").Row)
	assert.Equal(t, 2, w.s.Fleet.Count(bob, Ready))
	p = FriendLevel(w.s, FriendLevelReq{Seats: seats})
	assert.Empty(t, p.Units)
}

// friend level levels a one-shot friend as a batch one (docs/SPEC-SPRINT.md section 1, "A friend's
// card"): in either mode a friend fills up to her room (DealAhead times width), her width working.
func TestFriendLevelRespectsFriendDeliveryMode(t *testing.T) {
	t.Parallel()
	briefs := []string{friendBrief("friend amy")}
	for range 6 {
		briefs = append(briefs, friendBrief("friend"))
	}
	w := friendWorld(t, briefs...)
	// amy alone up at width 2: her room of 4 is filled, s1-1 (hers by name) and s1-2 working, s1-3 and s1-4 ready
	dealStarted(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash", Mode: config.FriendModeBatch})
	amy, bob := FriendRow("amy"), FriendRow("bob")
	require.Equal(t, 2, w.s.Fleet.Count(amy, Ready))
	require.Equal(t, 2, w.s.Fleet.Count(amy, Working))

	// bob is up in one-shot mode at width 2: his room is his width's, as a batch friend's
	// (one lane per unit of width, the owner 2026-10-10)
	seats := []FriendSeat{
		{Name: "amy", Width: 2, Status: Up, Class: "flash", Mode: config.FriendModeBatch},
		{Name: "bob", Width: 2, Status: Up, Class: "flash", Mode: config.FriendModeOneShot},
	}

	// amy's backlog is 4 - 2 = 2 and bob's is 0 - 2 = -2: both of amy's ready cards move
	// to bob, ready until he starts them; amy keeps her two working
	p := w.must(FriendLevel(w.s, FriendLevelReq{Seats: seats}))
	require.Len(t, p.Units, 2)
	assert.Equal(t, 0, w.s.Fleet.Count(bob, Working))
	assert.Equal(t, 2, w.s.Fleet.Count(bob, Ready), "a one-shot friend is levelled to his width")
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Working))
	assert.Equal(t, 0, w.s.Fleet.Count(amy, Ready))
	assert.Empty(t, Check(w.s, nil))

	// Running friend level again moves nothing
	p = FriendLevel(w.s, FriendLevelReq{Seats: seats})
	assert.Empty(t, p.Units)
}
