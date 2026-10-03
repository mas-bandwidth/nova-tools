package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's card (docs/SPEC-SPRINT.md section 1; the owner, 2026-10-03: "doing parts on
// friends where we would normally do friend work"): a brief whose header says WHO: friend
// is dealt by the tick to a friend up below her width, on her own fleet row, straight into
// working; a card with no WHO line is the machines' as before.

// friendBrief is a flash card brief whose header carries the WHO line who.
func friendBrief(who string) string { return tierBrief(who, "flash") }

// tierBrief is a card brief of the tier whose header carries the WHO line who.
func tierBrief(who, tier string) string {
	return "c: a friend's card tier: " + tier + "\nREPO: mas-bandwidth/nova-tools\nWHO: " + who + "\n\nThe task."
}

// everyTier is a friend who can do every tier.
var everyTier = []string{"flash", "frontier", "pro"}

// friendWorld is a world with two machines up and the cards of each brief given, one
// stream, ids s1-1, s1-2, ... in order.
func friendWorld(t *testing.T, briefs ...string) *world {
	w := newWorld(t, "reader-a", "reader-b")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
	var cards []CardAdd
	for i, b := range briefs {
		cards = append(cards, CardAdd{ID: "s1-" + itoa(i+1), Brief: b})
	}
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: cards}))
	return w
}

// dealWith is the tick's deal part with these friends, applied.
func dealWith(w *world, seats ...FriendSeat) Plan {
	w.t.Helper()
	p, _ := TickDeal(w.s, TickReq{Friends: seats})
	return w.must(p)
}

func TestAFriendsCardIsDealtToTheFriendItNamesOnHerRowInWorking(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	pr := w.s.Primary("s1-1")
	require.Equal(t, FriendRow("amy"), pr.F(FieldWho), "add writes the brief's WHO line on the card")

	// not up, or no width: it waits ready, and no machine is dealt it
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Down, Tiers: everyTier}, FriendSeat{Name: "bob", Width: 2, Status: Up, Tiers: everyTier})
	require.Equal(t, Ready, w.s.StateOf("s1-1"))
	require.Nil(t, w.s.Fleet.Card("s1-1.w1"))

	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Tiers: everyTier})
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Equal(t, FriendRow("amy"), wc.Row)
	assert.Equal(t, FriendRow("amy"), wc.F("member"))
	assert.Equal(t, Working, wc.Col, "nothing takes a friend's card: it is working once dealt")
	assert.Equal(t, "1", wc.F("gen"))
	assert.NotEmpty(t, wc.F("taken"), "its deadline is the working one, from its deal")
	assert.Equal(t, Working, w.s.StateOf("s1-1"))
	assert.Equal(t, "s1-1.w1", w.s.Primary("s1-1").F("work"))
	assert.NotContains(t, w.s.Members(), FriendRow("amy"), "her row is no machine of the fleet")
	assert.Empty(t, Check(w.s, nil), "what is always true holds with her card dealt")

	// the machines' deal verb refuses a friend's card by name
	w2 := friendWorld(t, friendBrief("friend"))
	p := Deal(w2.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "a friend's card")
}

func TestAFriendIsDealtNoMoreThanHerWidth(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend"))
	seats := []FriendSeat{{Name: "amy", Width: 2, Status: Up, Tiers: everyTier}, {Name: "bob", Width: 1, Status: Up, Tiers: everyTier}}
	dealWith(w, seats...)
	assert.Equal(t, 2, w.s.Fleet.Count(FriendRow("amy"), Working), "amy works her width")
	assert.Equal(t, 1, w.s.Fleet.Count(FriendRow("bob"), Working), "WHO: friend goes to the friend with room")
	assert.Equal(t, Ready, w.s.StateOf("s1-3"), "the third card for amy waits for a free lane")

	// a tick later nothing has freed: nothing more is dealt
	dealWith(w, seats...)
	assert.Equal(t, 2, w.s.Fleet.Count(FriendRow("amy"), Working))
	assert.Equal(t, Ready, w.s.StateOf("s1-3"))

	// she finishes one: the next tick deals her the third
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w.s, "s1-1.w1"), Head: "abc"}))
	dealWith(w, seats...)
	assert.Equal(t, Working, w.s.StateOf("s1-3"))
	assert.Equal(t, 2, w.s.Fleet.Count(FriendRow("amy"), Working))
}

func TestWhoFriendGoesToTheUpFriendWithTheMostFreeWidth(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"), friendBrief("friend"), friendBrief("friend"))
	dealWith(w, FriendSeat{Name: "amy", Width: 1, Status: Up, Tiers: everyTier}, FriendSeat{Name: "bob", Width: 3, Status: Up, Tiers: everyTier}, FriendSeat{Name: "cat", Width: 8, Status: Held, Tiers: everyTier})
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-1.w1").Row, "bob has three free, amy one")
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-2.w1").Row, "bob still has two free")
	assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-3.w1").Row, "amy and bob have one each: amy is first by name")
	assert.Equal(t, 0, w.s.Fleet.Count(FriendRow("cat"), Working), "a held friend is dealt nothing")
}

func TestACardWithNoWhoIsDealtToAMachine(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, "c: a machine's card\nREPO: mas-bandwidth/nova-tools\n\nThe task.")
	require.Empty(t, w.s.Primary("s1-1").F(FieldWho))
	dealWith(w, FriendSeat{Name: "amy", Width: 8, Status: Up, Tiers: everyTier})
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Contains(t, []string{"m1", "m2"}, wc.Row)
	assert.Equal(t, Ready, wc.Col, "a machine takes its card")
}

func TestAFriendWhoGoesQuietKeepsHerCardAndTheDeadlineHoldsIt(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Tiers: everyTier})
	require.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)

	// she goes quiet: the presence, the rebalance and the level move nothing of hers
	presenceWith(w, "m1", "m2")
	p, _ := TickLevel(w.s, TickReq{})
	w.must(p)
	wc := w.s.Fleet.Card("s1-1.w1")
	assert.Equal(t, FriendRow("amy"), wc.Row, "no take-back from a friend")
	assert.Equal(t, Working, wc.Col)
	assert.Equal(t, "1", wc.F("gen"))
	// she holds it before its deadline, whatever her status
	mustHold(t, running(w), "s1-1", HeldByActor)

	// past the working deadline the tick's late judgment names it, as any work card's
	w.tick(DeadlineUnfinished + time.Minute)
	p, _ = TickDeadlines(w.s, TickReq{})
	var late []Note
	for _, n := range p.Notes {
		if n.Type == NWorkLate {
			late = append(late, n)
		}
	}
	require.Len(t, late, 1)
	assert.Contains(t, late[0].What, "s1-1.w1")
}

func TestAReworkOfAFriendsCardWaitsReadyForAFriend(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Tiers: everyTier})
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: "friend amy HOLD: the gate is red"}))
	require.Equal(t, Review, w.s.StateOf("s1-1"))
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "make the gate green"}))
	require.Equal(t, Ready, w.s.StateOf("s1-1"), "a friend's rework is never dealt to a machine")
	require.Nil(t, w.s.Fleet.Card("s1-1.w2"))
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Tiers: everyTier})
	wc := w.s.Fleet.Card("s1-1.w2")
	require.NotNil(t, wc)
	assert.Equal(t, FriendRow("amy"), wc.Row)
	assert.Equal(t, "make the gate green", wc.F("fix"))
}

// A friend's row is no member of the clear's shape: a clear restores the machines, and
// her row, with no control card, comes back only when she is dealt to again.
func TestAFriendsRowIsNoMemberOfTheClearsShape(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Tiers: everyTier})
	require.True(t, w.s.Fleet.HasRow(FriendRow("amy")))
	sh := ShapeOf(w.s)
	assert.Equal(t, []string{"m1", "m2"}, sh.Members)
	assert.Equal(t, map[string]string{"m1": Up, "m2": Up}, sh.Status)
}
