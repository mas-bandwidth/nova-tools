package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's card goes only to a friend who can do it (docs/SPEC-SPRINT.md section 1; the
// owner, 2026-10-03 ~12:42 PM ET: "Now remember that some friends have weaker models.
// Freddy in particular is more like flash."; ~12:45 PM ET: "There is a responsibility to
// categorize cards for friends so they match to the set of friends who can do them,
// default all."): its tier is its category, and the friends who can take it are every
// friend whose tiers include it.

// tierSeats are three friends up, each of width 4: freddy does flash only, stella flash
// and pro, astra pro and frontier.
func tierSeats() []FriendSeat {
	return []FriendSeat{
		{Name: "freddy", Width: 4, Status: Up, Tiers: []string{"flash"}},
		{Name: "stella", Width: 4, Status: Up, Tiers: []string{"flash", "pro"}},
		{Name: "astra", Width: 4, Status: Up, Tiers: []string{"frontier", "pro"}},
	}
}

// friendTierJudgments is the plan's judgments of a friend's tier no friend can do.
func friendTierJudgments(p Plan) []Note {
	var out []Note
	for _, n := range p.Notes {
		if n.Type == NNoRoute && n.Kind == Judgment {
			out = append(out, n)
		}
	}
	return out
}

func TestAFlashCardIsDealtOnlyToAFriendWhoseTiersIncludeFlash(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, tierBrief("friend", "flash"), tierBrief("friend", "flash"), tierBrief("friend", "flash"))
	seats := tierSeats()
	seats[0].Width = 8 // freddy has the most free width: every flash card is his
	dealWith(w, seats...)
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		assert.Equal(t, FriendRow("freddy"), w.s.Fleet.Card(id+".w1").Row, "%s goes to the flash-capable friend with the most free width", id)
	}
	assert.Equal(t, []string{"freddy", "stella"}, FriendTakers(w.s.Primary("s1-1"), seats), "astra does not do flash")
	assert.Equal(t, 0, w.s.Fleet.Count(FriendRow("astra"), Working), "astra's tiers lack flash: she is dealt no flash card")
}

func TestAProCardIsNeverDealtToAFlashOnlyFriend(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, tierBrief("friend", "pro"), tierBrief("friend", "pro"))
	seats := tierSeats()
	seats[0].Width = 100 // freddy has all the room in the world
	seats[1].Width, seats[2].Width = 1, 1
	dealWith(w, seats...)
	assert.Equal(t, 0, w.s.Fleet.Count(FriendRow("freddy"), Working), "a pro card never goes to a flash-only friend")
	assert.Equal(t, FriendRow("astra"), w.s.Fleet.Card("s1-1.w1").Row, "astra and stella have one free each: astra is first by name")
	assert.Equal(t, FriendRow("stella"), w.s.Fleet.Card("s1-2.w1").Row)

	// a third pro card waits ready, though freddy is up with room: no judgment, a friend can take it
	w2 := friendWorld(t, tierBrief("friend", "pro"))
	only := []FriendSeat{{Name: "freddy", Width: 8, Status: Up, Tiers: []string{"flash"}}, {Name: "stella", Width: 1, Status: Down, Tiers: []string{"pro"}}}
	p := dealWith(w2, only...)
	assert.Equal(t, Ready, w2.s.StateOf("s1-1"), "the only pro friend is down: it waits for her")
	assert.Empty(t, friendTierJudgments(p), "a friend can take it when she is up: nothing to judge")
}

func TestANamedFriendIsDealtOnlyACardOfATierSheCanDo(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, tierBrief("friend freddy", "flash"), tierBrief("friend freddy", "pro"))
	p := dealWith(w, tierSeats()...)
	assert.Equal(t, FriendRow("freddy"), w.s.Fleet.Card("s1-1.w1").Row, "a flash card naming freddy is his")
	assert.Equal(t, Ready, w.s.StateOf("s1-2"), "a pro card naming freddy is never his, nor anyone else's")
	assert.Nil(t, w.s.Fleet.Card("s1-2.w1"))
	assert.Empty(t, FriendTakers(w.s.Primary("s1-2"), tierSeats()))
	js := friendTierJudgments(p)
	require.Len(t, js, 1, "a card no friend can take is judged")
	assert.Equal(t, FriendTierSubject("pro"), js[0].Stream)
	assert.Equal(t, []string{"s1-2"}, js[0].Primaries)
}

// A ready friend's card no friend can take raises the judgment of its tier, of the kind a
// tier no route serves: one per tier, naming the cards and every friend with her tiers,
// held on the card, never offering a route, and closed once a friend can take them.
func TestAFriendsCardNoFriendCanTakeRaisesTheJudgmentOfItsTier(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, tierBrief("friend", "frontier"), tierBrief("friend", "frontier"), tierBrief("friend", "flash"))
	seats := tierSeats()[:2] // freddy and stella: neither does frontier
	p := dealWith(w, seats...)
	js := friendTierJudgments(p)
	require.Len(t, js, 1, "one judgment for the tier, never one per card")
	j := js[0]
	assert.Equal(t, "tier:friend-frontier", j.Stream)
	assert.True(t, j.StreamLevel)
	assert.Equal(t, []string{"s1-1", "s1-2"}, j.Primaries)
	assert.Contains(t, j.What, "2 friend's cards of tier frontier wait")
	assert.Contains(t, j.What, "friends: freddy (flash), stella (flash,pro)")
	assert.Equal(t, []string{"look at the card", "drop", "wait"}, j.Decisions, "no route serves a friend: no route add")
	assert.Equal(t, Working, w.s.StateOf("s1-3"), "a card a friend can take is dealt beside it")

	// the card's hold names the judgment
	hd := mustHold(t, running(w), "s1-1", HeldByJudgment)
	assert.Contains(t, hd.Why, "no friend who may take it can do tier frontier")

	// a tick later the judgment stands, written once
	assert.Empty(t, friendTierJudgments(dealWith(w, seats...)))

	// astra joins with frontier: both are dealt and the judgment closes
	p = dealWith(w, tierSeats()...)
	assert.Equal(t, FriendRow("astra"), w.s.Fleet.Card("s1-1.w1").Row)
	assert.Equal(t, FriendRow("astra"), w.s.Fleet.Card("s1-2.w1").Row)
	require.Len(t, p.Closes, 1)
	assert.Equal(t, NNoRoute, p.Closes[0].Note.Type)
}

// A friend whose entry carries no tiers (a roster friend sync wrote before it copied
// them) takes no card until the next sync writes them.
func TestAFriendWithNoTiersTakesNoCard(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"))
	p := dealWith(w, FriendSeat{Name: "amy", Width: 8, Status: Up})
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	require.Len(t, friendTierJudgments(p), 1)
	assert.Contains(t, friendTierJudgments(p)[0].What, "friends: amy (none)")
}

// The card's tier is line 1's, or the tier the coordinator pinned; never the ladder's.
func TestAFriendsCardsTierIsItsLineOneOrItsPin(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, tierBrief("friend", "pro"), "c: no tier\nWHO: friend\n\nThe task.")
	assert.Equal(t, "pro", FriendTier(w.s.Primary("s1-1")))
	assert.Equal(t, "flash", FriendTier(w.s.Primary("s1-2")), "a card admitted with no tier (add refuses one now) is flash")
	c := &Card{ID: "s1-1", Fields: map[string]string{"brief": w.s.Primary("s1-1").F("brief"), FieldWho: WhoFriend, FieldTier: "frontier"}}
	assert.Equal(t, "frontier", FriendTier(c), "rework --tier pins it")
	c.Fields[FieldTier], c.Fields[FieldTierNow] = "", "flash"
	assert.Equal(t, "pro", FriendTier(c), "the flash-first ladder's tier is never a friend's card's")
	assert.Equal(t, []string{"stella"}, FriendTakersOf(FriendRow("stella"), "pro", tierSeats()))
	assert.Empty(t, FriendTakersOf(FriendRow("freddy"), "pro", tierSeats()))
	assert.Equal(t, []string{"astra", "stella"}, FriendTakersOf(WhoFriend, "pro", tierSeats()), "default all: every friend who can, in name order")
}
