package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The friends' deal and level go to idle lanes first (docs/SPEC-SPRINT.md section 1,
// friend-deal-idle-lanes-first.w1): eligibility is by the card's tier, never by class; a
// friend with an idle lane is preferred over every friend with none, then the most room;
// a friend at her room is never dealt; and every tick levels the friends after its deal,
// no verb run.

// fullWorld is a world whose friend amy holds her room at width 8: sixteen cards of brief
// who on her row, eight working and eight ready; then n cards for any friend, each of
// brief line 1 line1, added ready on the work table as s2-1, s2-2, ...
func fullWorld(t *testing.T, who, line1 string, n int) *world {
	briefs := make([]string, 16)
	for i := range briefs {
		briefs[i] = friendBrief(who)
	}
	w := friendWorld(t, briefs...)
	dealStarted(w, FriendSeat{Name: "amy", Width: 8, Status: Up, Class: "flash,pro"})
	require.Equal(t, 8, w.s.Fleet.Count(FriendRow("amy"), Working))
	require.Equal(t, 8, w.s.Fleet.Count(FriendRow("amy"), Ready))
	var cards []CardAdd
	for i := range n {
		cards = append(cards, CardAdd{ID: "s2-" + itoa(i+1), Brief: strings.Replace(friendBrief("friend"), "c: a friend's card", line1, 1)})
	}
	if n > 0 {
		w.must(Add(w.s, AddReq{Stream: "s2", Cards: cards}))
	}
	return w
}

func TestAFriendWithAnIdleLaneIsDealtAndLevelledBeforeAFullOne(t *testing.T) {
	t.Parallel()
	amy, bob, cat := FriendRow("amy"), FriendRow("bob"), FriendRow("cat")
	full := FriendSeat{Name: "amy", Width: 8, Status: Up, Class: "flash,pro"}

	t.Run("five new cards go to the two idle friends, none to the full one", func(t *testing.T) {
		t.Parallel()
		w := fullWorld(t, "only friend amy", "c: a friend's card", 5) // hard pins: the level never moves them
		dealStarted(w, full, FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"}, FriendSeat{Name: "cat", Width: 2, Status: Up, Class: "flash,pro"})
		// idle lanes first (bob 2, cat 2: bob by name), then the most idle lanes, then by
		// room when neither has a lane (bob 2, cat 2: bob by name)
		for i, want := range []string{bob, cat, bob, cat, bob} {
			wc := w.s.Fleet.Card("s2-" + itoa(i+1) + ".w1")
			require.NotNil(t, wc, "s2-%d is dealt", i+1)
			assert.Equal(t, want, wc.Row, "s2-%d", i+1)
		}
		assert.Equal(t, 16, friendLoad(w.s, "amy"), "amy at her room is dealt nothing")
		assert.Equal(t, 2, w.s.Fleet.Count(bob, Working))
		assert.Equal(t, 2, w.s.Fleet.Count(cat, Working))
		assert.Empty(t, Check(w.s, nil))
	})

	t.Run("an idle lane beats more room, and eligibility is the card's tier, never the class", func(t *testing.T) {
		t.Parallel()
		// 2026-10-05 9:40 AM: rowan-mas at working 8 of 8, ready 3 (room 16 - 11 = 5),
		// stella up at width 2 with both lanes idle (room 4) and of another class: a flash
		// card goes to stella; a pro card, a tier her row does not name, never does
		w := fullWorld(t, "only friend amy", "c: a friend's card", 0) // hard pins: the level never moves them
		mas := FriendSeat{Name: "mas", Width: 8, Status: Up, Class: "flash,heavy,pro"}
		stella := FriendSeat{Name: "stella", Width: 2, Status: Up, Class: "flash"}
		var hers []CardAdd
		for i := range 11 {
			hers = append(hers, CardAdd{ID: "s3-" + itoa(i+1), Brief: friendBrief("only friend mas")})
		}
		w.must(Add(w.s, AddReq{Stream: "s3", Cards: hers}))
		dealStarted(w, full, mas)
		require.Equal(t, 8, w.s.Fleet.Count(FriendRow("mas"), Working))
		require.Equal(t, 3, w.s.Fleet.Count(FriendRow("mas"), Ready))
		w.must(Add(w.s, AddReq{Stream: "s2", Cards: []CardAdd{
			{ID: "s2-1", Brief: friendBrief("friend")},
			{ID: "s2-2", Brief: strings.Replace(friendBrief("friend"), "c: a friend's card", "c: a friend's card tier: pro", 1)},
		}}))
		dealStarted(w, full, mas, stella)
		assert.Equal(t, FriendRow("stella"), w.s.Fleet.Card("s2-1.w1").Row, "an idle lane first, though mas has more room")
		assert.Equal(t, FriendRow("mas"), w.s.Fleet.Card("s2-2.w1").Row, "a pro card goes to a friend whose tiers hold pro")
		assert.Equal(t, 16, friendLoad(w.s, "amy"))
	})

	t.Run("a tick levels a backlog onto a friend coming up, without a verb", func(t *testing.T) {
		t.Parallel()
		// amy holds eight unstarted cards for any friend ready behind her full width, one of
		// them running by her beat; bob comes up idle at width 2, of another class
		w := fullWorld(t, "friend", "", 0)
		running := FriendSeat{Name: "amy", Width: 8, Status: Up, Class: "flash,pro", Running: []string{"s1-16.w1"}}
		bobUp := FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash"}
		dealStarted(w, running, FriendSeat{Name: "bob", Width: 2, Status: Down, Class: "flash"})
		require.Equal(t, 16, friendLoad(w.s, "amy"), "bob down: nothing moves")

		p := dealStarted(w, running, bobUp)
		assert.Equal(t, 2, w.s.Fleet.Count(bob, Working), "his idle lanes first")
		assert.Equal(t, 2, w.s.Fleet.Count(bob, Ready), "then evened to his room")
		assert.Equal(t, 12, friendLoad(w.s, "amy"))
		assert.Equal(t, amy, w.s.Fleet.Card("s1-16.w1").Row, "a card her beat names running stays")
		moved := 0
		for _, u := range p.Units {
			if strings.Contains(u.Moved, "friend.amy:ready -> friend.bob:") {
				moved++
			}
		}
		assert.Equal(t, 4, moved, "each move is one line")
		assert.Empty(t, dealStarted(w, running, bobUp).Units, "level: the next tick moves nothing")

		// a card moved off amy never goes back to her: bob's ready cards stay with him
		// while amy's lanes open and cat, idle, takes them
		w2 := fullWorld(t, "friend", "", 0)
		dealStarted(w2, running, bobUp)
		left := w2.s.Fleet.Cell(bob, Ready)
		require.Len(t, left, 2)
		for _, c := range left {
			assert.Equal(t, "amy", c.F(FieldFriendsLeft))
		}
		dealStarted(w2, FriendSeat{Name: "amy", Width: 16, Status: Up, Class: "flash,pro"}, FriendSeat{Name: "bob", Width: 1, Status: Up, Class: "flash"})
		for _, c := range left {
			assert.Equal(t, bob, w2.s.Fleet.Card(c.ID).Row, "%s was taken from amy: not levelled back to her", c.ID)
		}
		assert.Empty(t, Check(w.s, nil))
		assert.Empty(t, Check(w2.s, nil))
	})

	t.Run("the tick's level is bounded", func(t *testing.T) {
		t.Parallel()
		w := fullWorld(t, "friend", "", 0)
		p := dealStarted(w, full, FriendSeat{Name: "cat", Width: 16, Status: Up, Class: "flash"})
		assert.Equal(t, FriendLevelPerTick, w.s.Fleet.Count(cat, Working), "no more than FriendLevelPerTick moves a tick")
		assert.Len(t, p.Units, FriendLevelPerTick)
	})
}

// The owner's rule: a held or down friend's working and ready cards go to the up friends'
// ready queues. A card the level moved off amy onto bob carries friends_left=amy; when bob
// is held with his cards handed back, amy is the only friend up with room, and the card is
// dealt back to her rather than stranded ready (the chaos suite's hold case found it,
// internal/sprint/friend_chaos_functional_test.go). A friend up it never left is still preferred,
// and a card is never dealt back to the friend it was withdrawn from or taken back from.
func TestACardOffAHeldFriendGoesBackToAFriendTheLevelMovedItOff(t *testing.T) {
	t.Parallel()
	amy, bob, cat := FriendRow("amy"), FriendRow("bob"), FriendRow("cat")
	running := FriendSeat{Name: "amy", Width: 8, Status: Up, Class: "flash,pro", Running: []string{"s1-16.w1"}}
	bobUp := FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash"}
	// levelled: four of amy's cards go to bob, each carrying friends_left=amy
	levelled := func(t *testing.T) (*world, []string) {
		w := fullWorld(t, "friend", "", 0)
		dealStarted(w, running, bobUp)
		var his []string
		for _, col := range []string{Ready, Working} {
			for _, c := range w.s.Fleet.Cell(bob, col) {
				require.Equal(t, "amy", c.F(FieldFriendsLeft), "%s was levelled off amy", c.ID)
				his = append(his, c.ID)
			}
		}
		require.Len(t, his, 4)
		w.must(HoldNames(w.s, HoldReq{Names: []string{"bob"}, Return: true, Reason: "held", Who: "coordinator", Friends: []string{"amy", "bob", "cat"}}))
		require.Zero(t, friendLoad(w.s, "bob"), "the hold hands his cards back")
		return w, his
	}
	bobHeld := FriendSeat{Name: "bob", Width: 2, Status: Held, Class: "flash"}
	roomy := FriendSeat{Name: "amy", Width: 16, Status: Up, Class: "flash,pro", Running: []string{"s1-16.w1"}}

	t.Run("the only friend up with room is one the level moved them off", func(t *testing.T) {
		t.Parallel()
		w, his := levelled(t)
		dealStarted(w, roomy, bobHeld)
		for _, id := range his {
			wc := w.s.Fleet.Card(id)
			assert.Equal(t, amy, wc.Row, "%s off held bob is dealt back to amy, not stranded ready", id)
			assert.Equal(t, Working, w.s.StateOf(wc.F("primary")))
		}
		assert.Zero(t, friendLoad(w.s, "bob"))
		assert.Empty(t, Check(w.s, nil))
	})

	t.Run("a friend up it never left is preferred", func(t *testing.T) {
		t.Parallel()
		w, his := levelled(t)
		dealStarted(w, roomy, bobHeld, FriendSeat{Name: "cat", Width: 4, Status: Up, Class: "flash"})
		for _, id := range his {
			assert.Equal(t, cat, w.s.Fleet.Card(id).Row, "%s goes to cat, whom it never left, before amy", id)
		}
	})

	t.Run("never back to the friend it was taken back from", func(t *testing.T) {
		t.Parallel()
		// the coordinator takes bob's cards back (taken_from=bob) while amy is held: bob,
		// up, is the friend each was taken from and amy the one each left, so no friend
		// is dealt one; WHO is a preference, so each is the fleet's (docs/SPEC-SPRINT.md,
		// WHO preference)
		w := fullWorld(t, "friend", "", 0)
		dealStarted(w, running, bobUp)
		taken := w.must(FriendTake(w.s, FriendTakeReq{Friend: "bob", All: true, Reason: "slow", Who: "coordinator"}))
		require.NotEmpty(t, taken.Units)
		var his []string
		for _, c := range w.s.Fleet.Cell(bob, Withdrawn) {
			his = append(his, c.ID)
		}
		require.NotEmpty(t, his)
		dealStarted(w, bobUp, FriendSeat{Name: "amy", Width: 16, Status: Held, Class: "flash,pro"})
		for _, id := range his {
			wc := w.s.Fleet.Card(id)
			assert.False(t, IsFriendRow(wc.Row), "%s: taken back from bob, and amy is held: no friend, the fleet's (on %s)", id, wc.Row)
		}
	})
}
