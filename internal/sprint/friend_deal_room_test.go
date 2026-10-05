package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The friends' deal goes by room within a class, and the tick levels the friends after it
// (docs/SPEC-SPRINT.md section 1, friend-deal-most-room-now.w1): a friend at her room is dealt
// nothing while one of her class has room, a friend of another class is dealt nothing, and
// a backlog evens itself in a tick, no verb run.

// tickFriends is the tick's deal part with these friends and the cards they have started
// (nil: not read), applied.
func tickFriends(w *world, started map[string]string, seats ...FriendSeat) Plan {
	w.t.Helper()
	p, _ := TickDeal(w.s, TickReq{Friends: seats, FriendStarted: started})
	return w.must(p)
}

// fullWorld is a world whose friend amy holds her room, width 8: sixteen cards of brief
// who on her row, eight working and eight ready, and then n cards for any friend added
// ready on the work table.
func fullWorld(t *testing.T, who string, n int) *world {
	briefs := make([]string, 16)
	for i := range briefs {
		briefs[i] = friendBrief(who)
	}
	w := friendWorld(t, briefs...)
	dealWith(w, FriendSeat{Name: "amy", Width: 8, Status: Up, Class: "flash,pro"})
	require.Equal(t, 8, w.s.Fleet.Count(FriendRow("amy"), Working))
	require.Equal(t, 8, w.s.Fleet.Count(FriendRow("amy"), Ready))
	var cards []CardAdd
	for i := range n {
		cards = append(cards, CardAdd{ID: "s2-" + itoa(i+1), Brief: friendBrief("friend")})
	}
	if n > 0 {
		w.must(Add(w.s, AddReq{Stream: "s2", Cards: cards}))
	}
	return w
}

func TestFriendDealPrefersTheFriendWithTheMostRoom(t *testing.T) {
	t.Parallel()
	amy, bob, cat, dan := FriendRow("amy"), FriendRow("bob"), FriendRow("cat"), FriendRow("dan")

	t.Run("five new cards go to the idle two by room, none to the full one or another class", func(t *testing.T) {
		t.Parallel()
		w := fullWorld(t, "friend amy", 5)
		// room is DealAhead times width less working and ready: amy 0, bob 4, cat 8; dan,
		// of another class, has 32 and is dealt nothing while a friend of the cards' class has room
		tickFriends(w, nil,
			FriendSeat{Name: "amy", Width: 8, Status: Up, Class: "flash,pro"},
			FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"},
			FriendSeat{Name: "cat", Width: 4, Status: Up, Class: "flash,pro"},
			FriendSeat{Name: "dan", Width: 16, Status: Up, Class: "heavy"})
		for i, want := range []string{cat, cat, cat, cat, bob} {
			wc := w.s.Fleet.Card("s2-" + itoa(i+1) + ".w1")
			require.NotNil(t, wc)
			assert.Equal(t, want, wc.Row, "s2-%d: cat has the most room until bob and cat have four each, then bob is first by name", i+1)
		}
		assert.Equal(t, 16, friendLoad(w.s, "amy"), "amy at her room is dealt nothing")
		assert.Equal(t, 0, friendLoad(w.s, "dan"), "dan is of another class")
		assert.Empty(t, Check(w.s, nil))
	})

	t.Run("a card no friend's class holds goes to any friend by room", func(t *testing.T) {
		t.Parallel()
		w := fullWorld(t, "friend amy", 1)
		tickFriends(w, nil, FriendSeat{Name: "amy", Width: 8, Status: Up, Class: "heavy"}, FriendSeat{Name: "dan", Width: 1, Status: Up, Class: "heavy"})
		assert.Equal(t, dan, w.s.Fleet.Card("s2-1.w1").Row)
	})

	t.Run("a tick evens a backlog without a verb", func(t *testing.T) {
		t.Parallel()
		w := fullWorld(t, "friend", 0)
		seats := []FriendSeat{{Name: "amy", Width: 8, Status: Up, Class: "flash,pro"}, {Name: "bob", Width: 8, Status: Up, Class: "flash,pro"}}
		p := tickFriends(w, nil, seats...)
		assert.Empty(t, p.Units, "with what she has started not read, the tick levels no friend")
		started := map[string]string{"s1-16.w1": "a push on its branch"}
		p = tickFriends(w, started, seats...)
		require.NotEmpty(t, p.Units)
		assert.Contains(t, p.Units[0].Moved, "moved=7 to bob(7) from amy(7)", "backlogs 8 and -8 even to 1 and -1: the started card stays")
		assert.Equal(t, amy, w.s.Fleet.Card("s1-16.w1").Row)
		assert.Equal(t, 9, friendLoad(w.s, "amy"))
		assert.Equal(t, 7, w.s.Fleet.Count(bob, Working), "bob had his lanes free")
		assert.Empty(t, tickFriends(w, started, seats...).Units, "even: the next tick moves nothing")
		assert.Empty(t, Check(w.s, nil))
	})

	t.Run("the level after the deal counts what the deal placed", func(t *testing.T) {
		t.Parallel()
		w := fullWorld(t, "friend", 4)
		tickFriends(w, map[string]string{}, FriendSeat{Name: "amy", Width: 8, Status: Up, Class: "flash,pro"}, FriendSeat{Name: "bob", Width: 8, Status: Up, Class: "flash,pro"})
		// the deal gives bob the four new cards (room 16 against 0); the level then counts
		// them: backlogs 8 and -4 even at 2 and 2, six moving, and bob works his width
		assert.Equal(t, 10, friendLoad(w.s, "amy"))
		assert.Equal(t, 10, friendLoad(w.s, "bob"))
		assert.Equal(t, 8, w.s.Fleet.Count(bob, Working), "never past his width")
		assert.Equal(t, 2, w.s.Fleet.Count(bob, Ready))
		assert.Equal(t, 0, friendLoad(w.s, "cat"))
		assert.Empty(t, Check(w.s, nil))
	})
}
