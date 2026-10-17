package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's card (docs/SPEC-SPRINT.md section 1; the owner, 2026-10-03: "doing parts on
// friends where we would normally do friend work"): a brief whose header says WHO: friend
// is offered by the tick to a friend with room, on her own fleet row. Only
// an explicit "only friend" pin prevents fallback to another eligible worker.

// friendBrief is a card brief whose header carries the WHO line who.
func friendBrief(who string) string {
	return "c: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: " + who + "\n\nThe task."
}

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
	w := friendWorld(t, friendBrief("only friend amy"))
	pr := w.s.Primary("s1-1")
	require.Equal(t, "only."+FriendRow("amy"), pr.F(FieldWho), "add writes the brief's WHO line on the card")

	// not up, or no width: it waits ready, and no machine is dealt it
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Down}, FriendSeat{Name: "bob", Width: 2, Status: Up})
	require.Equal(t, Ready, w.s.StateOf("s1-1"))
	require.Nil(t, w.s.Fleet.Card("s1-1.w1"))

	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up})
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
	w2 := friendWorld(t, friendBrief("only friend amy"))
	p := Deal(w2.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "a friend's card")
}

func TestAFriendIsDealtHerRoomWorkingAtHerWidthAndReadyBehind(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("only friend amy"), friendBrief("only friend amy"), friendBrief("only friend amy"), friendBrief("only friend amy"), friendBrief("only friend amy"), friendBrief("friend"))
	seats := []FriendSeat{{Name: "amy", Width: 2, Status: Up}, {Name: "bob", Width: 1, Status: Up, Class: "flash"}}
	dealWith(w, seats...)
	amy := FriendRow("amy")
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Working), "amy works her width")
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Ready), "and holds as many again ready behind them (DealAhead times her width)")
	assert.Equal(t, 1, w.s.Fleet.Count(FriendRow("bob"), Working), "WHO: friend goes to the friend with room")
	assert.Equal(t, Ready, w.s.StateOf("s1-5"), "the fifth card for amy waits for room on her row")
	ready := w.s.Fleet.Card("s1-3.w1")
	assert.Equal(t, Ready, ready.Col)
	assert.Empty(t, ready.F("taken"), "a card dealt ready is not taken")
	assert.Equal(t, Working, w.s.StateOf("s1-3"), "its primary is working on it, as a machine's dealt primary is")

	// a tick later nothing has freed: nothing more is dealt
	dealWith(w, seats...)
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Working))
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Ready))
	assert.Equal(t, Ready, w.s.StateOf("s1-5"))

	// she finishes one: her finish takes her oldest ready card into working at once, no
	// tick between, and the next tick deals her the fifth, ready behind
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: amy, Gens: gensOf(w.s, "s1-1.w1"), Head: "abc"}))
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Working), "her next card is working the moment one finishes")
	assert.Equal(t, Working, w.s.Fleet.Card("s1-3.w1").Col, "the oldest ready one")
	assert.NotEmpty(t, w.s.Fleet.Card("s1-3.w1").F("taken"))
	assert.Equal(t, 1, w.s.Fleet.Count(amy, Ready))
	dealWith(w, seats...)
	assert.Equal(t, Working, w.s.StateOf("s1-5"))
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Ready), "the deal fills her room again")
	assert.Empty(t, Check(w.s, nil))
}

// The owner's example (2026-10-04): a friend at width 8 with 30 cards waiting has 16
// dealt, 8 working and 8 ready, and gets a 17th when one lands.
func TestAFriendAtWidthEightWithThirtyCardsHasSixteenDealt(t *testing.T) {
	t.Parallel()
	briefs := make([]string, 30)
	for i := range briefs {
		briefs[i] = friendBrief("only friend amy")
	}
	w := friendWorld(t, briefs...)
	seat := FriendSeat{Name: "amy", Width: 8, Status: Up}
	dealWith(w, seat)
	amy := FriendRow("amy")
	require.Equal(t, 8, w.s.Fleet.Count(amy, Working))
	require.Equal(t, 8, w.s.Fleet.Count(amy, Ready))
	assert.Equal(t, 14, len(w.s.Work.Column(Ready)), "fourteen wait on the work table")
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: amy, Gens: gensOf(w.s, "s1-1.w1"), Head: "abc"}))
	assert.Equal(t, 8, w.s.Fleet.Count(amy, Working), "she took her next at once")
	assert.Equal(t, 7, w.s.Fleet.Count(amy, Ready))
	dealWith(w, seat)
	assert.Equal(t, 8, w.s.Fleet.Count(amy, Ready), "the 17th is dealt")
	assert.Equal(t, 13, len(w.s.Work.Column(Ready)))
}

func TestWhoFriendGoesToTheUpFriendWithTheMostFreeWidth(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"), friendBrief("friend"), friendBrief("friend"))
	// room is DealAhead times width: amy has two places, bob four
	dealWith(w, FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash"}, FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash"}, FriendSeat{Name: "cat", Width: 8, Status: Held})
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-1.w1").Row, "bob has four free, amy two")
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-2.w1").Row, "bob still has three free")
	assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-3.w1").Row, "amy and bob have two each: amy is first by name")
	assert.Equal(t, 0, w.s.Fleet.Count(FriendRow("cat"), Working), "a held friend is dealt nothing")
}

func TestACardWithNoWhoIsDealtToAMachine(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, "c: a machine's card\nREPO: mas-bandwidth/nova-tools\n\nThe task.")
	require.Empty(t, w.s.Primary("s1-1").F(FieldWho))
	dealWith(w, FriendSeat{Name: "amy", Width: 8, Status: Up})
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Contains(t, []string{"m1", "m2"}, wc.Row)
	assert.Equal(t, Ready, wc.Col, "a machine takes its card")
}

func TestAFriendWhoGoesQuietKeepsHerCardAndTheDeadlineHoldsIt(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up})
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
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up})
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: "friend amy HOLD: the gate is red"}))
	require.Equal(t, Review, w.s.StateOf("s1-1"))
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "make the gate green"}))
	require.Equal(t, Ready, w.s.StateOf("s1-1"), "a friend's rework is never dealt to a machine")
	require.Nil(t, w.s.Fleet.Card("s1-1.w2"))
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up})
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
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up})
	require.True(t, w.s.Fleet.HasRow(FriendRow("amy")))
	sh := ShapeOf(w.s)
	assert.Equal(t, []string{"m1", "m2"}, sh.Members)
	assert.Equal(t, map[string]string{"m1": Up, "m2": Up}, sh.Status)
}
