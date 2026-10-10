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

// friend level respects a friend's delivery mode (docs/SPEC-SPRINT.md section 1, "A friend's card"):
// in batch mode (the default), a friend fills up to room (DealAhead times width); in one-shot mode,
// a friend's room is 1 and width is 1, so she takes at most one card into working and holds no ready cards.
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

	// bob is up in one-shot mode (width 2, but mode one-shot gives room 1 and width 1)
	seats := []FriendSeat{
		{Name: "amy", Width: 2, Status: Up, Class: "flash", Mode: config.FriendModeBatch},
		{Name: "bob", Width: 2, Status: Up, Class: "flash", Mode: config.FriendModeOneShot},
	}

	// amy's backlog is 4 - 2 = 2. bob's backlog is 0 - 1 = -1.
	// 1 card moves to bob, ready until he starts it (room 1, width 1).
	// bob now holds 1 (his room is full).
	// amy holds 3 (backlog 1). bob holds 1 (backlog 0).
	// No more cards move to bob because bob has reached his room of 1.
	p := w.must(FriendLevel(w.s, FriendLevelReq{Seats: seats}))
	require.Len(t, p.Units, 1)
	assert.Equal(t, 0, w.s.Fleet.Count(bob, Working))
	assert.Equal(t, 1, w.s.Fleet.Count(bob, Ready), "a one-shot friend holds one card, ready until he starts it")
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Working))
	assert.Equal(t, 1, w.s.Fleet.Count(amy, Ready), "amy keeps her remaining ready card")
	assert.Empty(t, Check(w.s, nil))

	// Running friend level again moves nothing
	p = FriendLevel(w.s, FriendLevelReq{Seats: seats})
	assert.Empty(t, p.Units)
}
