package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
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
	dealStarted(w, FriendSeat{Name: "amy", Width: 2, Status: Down}, FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"})
	require.Equal(t, Ready, w.s.StateOf("s1-1"))
	require.Nil(t, w.s.Fleet.Card("s1-1.w1"))

	amy := FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash,pro"}
	dealWith(w, amy)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Equal(t, FriendRow("amy"), wc.Row)
	assert.Equal(t, FriendRow("amy"), wc.F("member"))
	assert.Equal(t, Ready, wc.Col, "a friend's card is ready on her row until she starts it")
	assert.Empty(t, wc.F("taken"))
	startLanes(w, amy)
	wc = w.s.Fleet.Card("s1-1.w1")
	assert.Equal(t, Working, wc.Col, "working once she starts it")
	assert.Equal(t, "1", wc.F("gen"))
	assert.NotEmpty(t, wc.F("taken"), "its deadline is the working one, from her start")
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
	seats := []FriendSeat{{Name: "amy", Width: 2, Status: Up, Class: "flash,pro"}, {Name: "bob", Width: 1, Status: Up, Class: "flash"}}
	dealStarted(w, seats...)
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
	dealStarted(w, seats...)
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
	dealStarted(w, seats...)
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
	seat := FriendSeat{Name: "amy", Width: 8, Status: Up, Class: "flash,pro"}
	dealStarted(w, seat)
	amy := FriendRow("amy")
	require.Equal(t, 8, w.s.Fleet.Count(amy, Working))
	require.Equal(t, 8, w.s.Fleet.Count(amy, Ready))
	assert.Equal(t, 14, len(w.s.Work.Column(Ready)), "fourteen wait on the work table")
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: amy, Gens: gensOf(w.s, "s1-1.w1"), Head: "abc"}))
	assert.Equal(t, 8, w.s.Fleet.Count(amy, Working), "she took her next at once")
	assert.Equal(t, 7, w.s.Fleet.Count(amy, Ready))
	dealStarted(w, seat)
	assert.Equal(t, 8, w.s.Fleet.Count(amy, Ready), "the 17th is dealt")
	assert.Equal(t, 13, len(w.s.Work.Column(Ready)))
}

func TestWhoFriendGoesToTheUpFriendWithTheMostFreeWidth(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"), friendBrief("friend"), friendBrief("friend"))
	// room is DealAhead times width: amy has two places, bob four
	dealStarted(w, FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash"}, FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash"}, FriendSeat{Name: "cat", Width: 8, Status: Held})
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-1.w1").Row, "bob has four free, amy two")
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-2.w1").Row, "bob still has three free")
	assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-3.w1").Row, "amy and bob have two each: amy is first by name")
	assert.Equal(t, 0, w.s.Fleet.Count(FriendRow("cat"), Working), "a held friend is dealt nothing")
}

func TestACardWithNoWhoIsDealtToAMachine(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, "c: a machine's card\nREPO: mas-bandwidth/nova-tools\n\nThe task.")
	require.Empty(t, w.s.Primary("s1-1").F(FieldWho))
	dealStarted(w, FriendSeat{Name: "amy", Width: 8, Status: Up})
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Contains(t, []string{"m1", "m2"}, wc.Row)
	assert.Equal(t, Ready, wc.Col, "a machine takes its card")
}

func TestAFriendWhoGoesQuietKeepsHerCardAndTheDeadlineHoldsIt(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	dealStarted(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash,pro"})
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
	dealStarted(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash,pro"})
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: "friend amy HOLD: the gate is red"}))
	require.Equal(t, Review, w.s.StateOf("s1-1"))
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "make the gate green"}))
	require.Equal(t, Ready, w.s.StateOf("s1-1"), "a friend's rework is never dealt to a machine")
	require.Nil(t, w.s.Fleet.Card("s1-1.w2"))
	dealStarted(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash,pro"})
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
	dealStarted(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash,pro"})
	require.True(t, w.s.Fleet.HasRow(FriendRow("amy")))
	sh := ShapeOf(w.s)
	assert.Equal(t, []string{"m1", "m2"}, sh.Members)
	assert.Equal(t, map[string]string{"m1": Up, "m2": Up}, sh.Status)
}

// Dealing respects the friend row's delivery mode from nova-config: mode: batch|one-shot
// on the friend row (docs/SPEC-SPRINT.md section 1, "A friend's card"). In batch mode
// (the default) the machine deals up to the row's width
// as today (DealAhead times width, working at her width and ready behind); in one-shot mode
// it fills her configured width without leaving ready cards behind.
func TestDealingRespectsAFriendDeliveryMode(t *testing.T) {
	t.Parallel()
	// Amy is batch (the default, width 2), Bob is one-shot (width 2, mode: one-shot).
	// Amy with 4 cards waiting gets 2 working and 2 ready behind.
	// Bob with 4 cards waiting gets 2 working and 0 ready; the other 2 wait on the work table.
	w := friendWorld(t,
		friendBrief("only friend amy"), friendBrief("only friend amy"), friendBrief("only friend amy"), friendBrief("only friend amy"),
		friendBrief("only friend bob"), friendBrief("only friend bob"), friendBrief("only friend bob"), friendBrief("only friend bob"),
	)
	seats := []FriendSeat{
		{Name: "amy", Width: 2, Status: Up, Mode: config.FriendModeBatch, Class: "flash,pro"},
		{Name: "bob", Width: 2, Status: Up, Mode: config.FriendModeOneShot, Class: "flash,pro"},
	}
	dealStarted(w, seats...)

	amy := FriendRow("amy")
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Working), "amy works her width")
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Ready), "amy holds 2 ready behind (batch mode)")

	bob := FriendRow("bob")
	assert.Equal(t, 2, w.s.Fleet.Count(bob, Working), "bob fills his configured one-shot width")
	assert.Equal(t, 0, w.s.Fleet.Count(bob, Ready), "bob holds no cards ready behind in one-shot mode")
	assert.Equal(t, Ready, w.s.StateOf("s1-7"), "the 3rd card for bob waits ready on the work table")
	assert.Equal(t, Ready, w.s.StateOf("s1-8"), "the 4th card for bob waits ready on the work table")

	// A tick later without bob finishing: nothing more is dealt to bob
	dealStarted(w, seats...)
	assert.Equal(t, 2, w.s.Fleet.Count(bob, Working))
	assert.Equal(t, 0, w.s.Fleet.Count(bob, Ready))
	assert.Equal(t, Ready, w.s.StateOf("s1-7"))

	// Bob finishes his first card; the third card is still on the work table.
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-5.w1"}}, As: bob, Gens: gensOf(w.s, "s1-5.w1"), Head: "abc"}))
	assert.Equal(t, 1, w.s.Fleet.Count(bob, Working), "the other started card remains active")

	// The next deal/tick leaves bob at configured width.
	dealStarted(w, seats...)
	assert.Equal(t, 2, w.s.Fleet.Count(bob, Working), "the next card is dealt and started in the free slot")
	assert.Equal(t, 0, w.s.Fleet.Count(bob, Ready))
	assert.Equal(t, Working, w.s.StateOf("s1-7"))
	assert.Equal(t, Ready, w.s.StateOf("s1-8"))

	// WHO: friend (any friend) prefers idle lanes first, then room, then name.
	// Amy batch width 2 (lanes 2, room 4) and Bob one-shot width 2 (lanes 2, room 2)
	// are dealt amy, bob, amy, bob, amy: each placed card holds a lane.
	w2 := friendWorld(t, friendBrief("friend"), friendBrief("friend"), friendBrief("friend"), friendBrief("friend"), friendBrief("friend"))
	dealStarted(w2,
		FriendSeat{Name: "amy", Width: 2, Status: Up, Mode: config.FriendModeBatch, Class: "flash"},
		FriendSeat{Name: "bob", Width: 2, Status: Up, Mode: config.FriendModeOneShot, Class: "flash"},
	)
	assert.Equal(t, 2, w2.s.Fleet.Count(amy, Working), "amy gets 2 working")
	assert.Equal(t, 1, w2.s.Fleet.Count(amy, Ready), "amy gets 1 ready (idle lanes first, not her room 4)")
	assert.Equal(t, 2, w2.s.Fleet.Count(bob, Working), "bob's idle lanes take two cards, both started")
	assert.Equal(t, 0, w2.s.Fleet.Count(bob, Ready), "bob holds none ready")
}

// The one-shot friend sorts first by name. Room still breaks every lanes tie before
// name (preferredFriend: an idle lane, then free room, then the first by name), so
// the counts match the unswapped seats above. A placed card holds a lane, started
// or not (friendDeal: lanes is her width less friendLoad).
//
// Amy is one-shot width 2 (lanes 2, room 2) and Bob is batch width 2 (lanes 2,
// room 4). Five WHO: friend cards, both idle:
//
//	1 bob  lanes tied at 2; bob's free 4 beats amy's 2
//	2 amy  amy has 2 idle lanes, bob 1
//	3 bob  lanes tied at 1; bob's free 3 beats amy's 1
//	4 amy  amy has 1 idle lane, bob 0
//	5 bob  lanes tied at 0, and amy has no free room, so only bob may take it
//
// Bob is dealt 3 and Amy 2. After each starts the cards her free lanes hold, the
// batch friend is working 2 ready 1 and the one-shot friend is working 2 ready 0.
func TestDealingRespectsAFriendDeliveryModeWhenTheOneShotFriendSortsFirst(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"), friendBrief("friend"), friendBrief("friend"), friendBrief("friend"), friendBrief("friend"))
	dealStarted(w,
		FriendSeat{Name: "amy", Width: 2, Status: Up, Mode: config.FriendModeOneShot, Class: "flash"},
		FriendSeat{Name: "bob", Width: 2, Status: Up, Mode: config.FriendModeBatch, Class: "flash"},
	)
	amy := FriendRow("amy")
	bob := FriendRow("bob")
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Working), "amy starts both cards her one-shot lanes were dealt")
	assert.Equal(t, 0, w.s.Fleet.Count(amy, Ready), "amy holds none ready: room broke the tie, not her name")
	assert.Equal(t, 2, w.s.Fleet.Count(bob, Working), "bob starts his width")
	assert.Equal(t, 1, w.s.Fleet.Count(bob, Ready), "bob holds the third card ready; his batch room is 4")
}

// A read already holding a lane changes who receives the next work cards
// (docs/SPEC-SPRINT.md friend-deal-idle-lanes-first; a lane is idle while no card
// on her row holds it, friendDeal). putReads leaves one read working on amy.
// Read cards are off, so friendLoad counts that card as one (ready + working)
// and her idle lanes are her width less that load.
//
// The seats are the unswapped pair: amy batch width 2 (room 4) and bob one-shot
// width 2 (room 2). With no read, lanes tie and amy's room takes the first card
// (amy, bob, amy, bob, amy). The read leaves amy 1 idle lane and 3 free, bob 2 and 2:
//
//	1 bob  bob has more idle lanes (2 > 1), so the first card leaves amy
//	2 amy  lanes tied at 1; amy's free 3 beats bob's 1
//	3 bob  bob has the only idle lane
//	4 amy  bob has no free room
//	5 amy  amy still has one free
//
// Amy's read is already working, so she starts only one of her three work cards
// (her width less what is working). Bob starts both of his.
func TestAReadHoldingALaneChangesWhoReceivesTheNextWorkCards(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"), friendBrief("friend"), friendBrief("friend"), friendBrief("friend"), friendBrief("friend"))
	putReads(w, "amy", 1)
	amy := FriendRow("amy")
	bob := FriendRow("bob")
	require.Equal(t, 1, w.s.Fleet.Count(amy, Working), "the read is working on amy before the deal")

	dealStarted(w,
		FriendSeat{Name: "amy", Width: 2, Status: Up, Mode: config.FriendModeBatch, Class: "flash"},
		FriendSeat{Name: "bob", Width: 2, Status: Up, Mode: config.FriendModeOneShot, Class: "flash"},
	)

	got := map[string][2]string{}
	for _, id := range []string{"s1-1.w1", "s1-2.w1", "s1-3.w1", "s1-4.w1", "s1-5.w1"} {
		c := w.s.Fleet.Card(id)
		require.NotNil(t, c, id)
		got[id] = [2]string{c.Row, c.Col}
	}
	assert.Equal(t, map[string][2]string{
		"s1-1.w1": {bob, Working},
		"s1-2.w1": {amy, Working},
		"s1-3.w1": {bob, Working},
		"s1-4.w1": {amy, Ready},
		"s1-5.w1": {amy, Ready},
	}, got)
	rc := w.s.Fleet.Card(ReadCardID("read-0", 1, "amy"))
	require.NotNil(t, rc)
	assert.Equal(t, amy, rc.Row)
	assert.Equal(t, Working, rc.Col, "the read stays on the lane it held")
	assert.Equal(t, 2, w.s.Fleet.Count(bob, Working))
	assert.Equal(t, 0, w.s.Fleet.Count(bob, Ready))
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Working), "one work card started, and the read")
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Ready))
}

// In one-shot mode, friendNext fills a newly free configured slot while counting shared
// work/read occupancy. A full mixed row blocks further queued promotion.
func TestFriendNextFillsConfiguredOneShotWidthAcrossWorkAndRead(t *testing.T) {
	t.Parallel()
	briefs := []string{
		friendBrief("friend amy"),
		friendBrief("friend amy"),
		friendBrief("friend amy"),
		friendBrief("friend amy"),
	}
	w := friendWorld(t, briefs...)
	// amy initially dealt in batch mode at width 2: s1-1 and s1-2 working, s1-3 and s1-4 ready
	dealStarted(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Mode: config.FriendModeBatch, Class: "flash,pro"})
	amy := FriendRow("amy")
	require.Equal(t, 2, w.s.Fleet.Count(amy, Working))
	require.Equal(t, 2, w.s.Fleet.Count(amy, Ready))

	// Amy switches to one-shot mode:
	seats := []FriendSeat{
		{Name: "amy", Width: 2, Status: Up, Mode: config.FriendModeOneShot, Class: "flash,pro"},
	}

	// Finishing s1-1.w1 leaves one slot free, so the oldest ready card advances.
	w.must(Finish(w.s, FinishReq{
		Sel:     Sel{IDs: []string{"s1-1.w1"}},
		As:      amy,
		Gens:    gensOf(w.s, "s1-1.w1"),
		Head:    "abc",
		Friends: seats,
	}))
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Working), "s1-3 fills the free configured slot")
	assert.Equal(t, 1, w.s.Fleet.Count(amy, Ready), "one ready card remains after filling width")
	assert.Equal(t, Working, w.s.Fleet.Card("s1-2.w1").Col)
	assert.Equal(t, Working, w.s.Fleet.Card("s1-3.w1").Col)
	assert.Equal(t, Ready, w.s.Fleet.Card("s1-4.w1").Col)

	// Now place an active frontier read card on amy's row to test shared work/read occupancy
	w.s.Fleet.Put(&Card{
		ID:  "read-1",
		Row: amy,
		Col: Working,
		Rev: 1,
		Fields: map[string]string{
			"kind": "read", "primary": "s1-2", "stream": "s1", "reader": "amy",
		},
	})

	// Finish s1-2.w1: read-1 and s1-3 now fill the configured width, so no further
	// promotion is possible.
	w.must(Finish(w.s, FinishReq{
		Sel:     Sel{IDs: []string{"s1-2.w1"}},
		As:      amy,
		Gens:    gensOf(w.s, "s1-2.w1"),
		Head:    "def",
		Friends: seats,
	}))
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Working), "read-1 and s1-3.w1 are still active")
	assert.Equal(t, 1, w.s.Fleet.Count(amy, Ready), "queued promotion gated by full mixed occupancy")
	assert.Equal(t, Working, w.s.Fleet.Card("s1-3.w1").Col)

	// Retire the read card (simulating friend read close)
	rc := w.s.Fleet.Card("read-1")
	rc.Col = DoneOK
	w.s.Fleet.Put(rc)

	// Now place s1-3.w1 into working so it can finish and trigger promotion with 0 remaining active
	c3 := w.s.Fleet.Card("s1-3.w1")
	c3.Col = Working
	w.s.Fleet.Put(c3)
	pr3 := w.s.Work.Card("s1-3")
	pr3.Col = Working
	pr3.Fields["work"] = "s1-3.w1"
	w.s.Work.Put(pr3)

	w.must(Finish(w.s, FinishReq{
		Sel:     Sel{IDs: []string{"s1-3.w1"}},
		As:      amy,
		Gens:    gensOf(w.s, "s1-3.w1"),
		Head:    "ghi",
		Friends: seats,
	}))
	// Now occupancy reached zero when s1-3.w1 finished, so s1-4.w1 advances into Working!
	assert.Equal(t, 1, w.s.Fleet.Count(amy, Working), "promoted exactly one card")
	assert.Equal(t, 0, w.s.Fleet.Count(amy, Ready), "no cards left in ready")
	assert.Equal(t, Working, w.s.Fleet.Card("s1-4.w1").Col)
}

// The deal fills only a row that can work (docs/SPEC-SPRINT.md section 1, a friend's card):
// a friend whose status is not up (her beat alone is no evidence) or whom the stall ladder
// marked down is dealt nothing; and a card whose WHO line names a friend up with room goes
// to her, even when another friend has more room. A WHO: friend <name> card whose friend is
// not up is offered on, as the preference it is, with the pin-ignored judgment naming why
// (2026-10-06: a friend whose row read down, her daemon beating, was dealt 18 cards twice).
func TestTheDealSkipsADownRowAndHonoursTheWhoPin(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend bob"), friendBrief("friend"), friendBrief("friend"))
	// cat is up by her session's evidence, but the stall ladder marked her down
	w.s.Fleet.SetProp(PropFriendStallDown("cat"), stamp(w.s.Now.Add(-time.Minute)))
	seats := []FriendSeat{
		{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"},
		{Name: "bob", Width: 4, Status: Down, Class: "flash,pro", Why: "no session evidence; her beat 1s ago is not evidence"},
		{Name: "cat", Width: 4, Status: Up, Class: "flash,pro"},
		{Name: "dan", Width: 4, Status: Up, Class: "flash,pro"},
	}
	p := dealWith(w, seats...)

	assert.Zero(t, w.s.Fleet.Count(FriendRow("bob"), Ready)+w.s.Fleet.Count(FriendRow("bob"), Working), "a down row, beating, is dealt nothing")
	assert.Zero(t, w.s.Fleet.Count(FriendRow("cat"), Ready)+w.s.Fleet.Count(FriendRow("cat"), Working), "a row the stall ladder marked down is dealt nothing")
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Equal(t, FriendRow("amy"), wc.Row, "WHO: friend amy goes to amy, up with room, though dan has more")
	wc = w.s.Fleet.Card("s1-2.w1")
	require.NotNil(t, wc)
	assert.NotEqual(t, FriendRow("bob"), wc.Row)
	assert.NotEqual(t, FriendRow("cat"), wc.Row)
	var ignored []Note
	for _, u := range p.Units {
		for _, n := range u.Notes {
			if n.Type == NPinIgnored {
				ignored = append(ignored, n)
			}
		}
	}
	require.Len(t, ignored, 1, "one judgment, on the card whose friend is not up")
	assert.Contains(t, ignored[0].What, "s1-2")
	assert.Contains(t, ignored[0].What, "bob did not take it because she is not up")
	for _, id := range []string{"s1-3.w1", "s1-4.w1"} {
		c := w.s.Fleet.Card(id)
		require.NotNil(t, c, id)
		assert.Contains(t, []string{FriendRow("amy"), FriendRow("dan")}, c.Row, "%s goes to a row that can work", id)
	}
}

// A friend whose status is up is still not dealt when her fleet control card says
// down or held (friendDealable): the row cannot work, whatever her session says.
// On 2026-10-06 a friend whose row read down, her lanes paused and her daemon
// beating, was dealt 18 cards twice.
func TestAFriendUpWhoseControlCardIsHeldOrDownIsNotDealt(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"), friendBrief("friend"), friendBrief("friend"))
	putCtl := func(name, status, held string) {
		t.Helper()
		row := FriendRow(name)
		w.s.Fleet.Put(&Card{
			ID: CtlID(row), Row: row, Col: Ctl, Rev: 1,
			Fields: map[string]string{"kind": "member", "status": status, "held": held},
		})
	}
	putCtl("amy", Down, "")
	putCtl("bob", Held, "")
	putCtl("cat", Up, stamp(w.s.Now))
	seats := []FriendSeat{
		{Name: "amy", Width: 4, Status: Up, Class: "flash,pro"},
		{Name: "bob", Width: 4, Status: Up, Class: "flash,pro"},
		{Name: "cat", Width: 4, Status: Up, Class: "flash,pro"},
		{Name: "dan", Width: 4, Status: Up, Class: "flash,pro"},
	}
	dealWith(w, seats...)

	for _, name := range []string{"amy", "bob", "cat"} {
		n := w.s.Fleet.Count(FriendRow(name), Ready) + w.s.Fleet.Count(FriendRow(name), Working)
		assert.Zero(t, n, "friend %s is up, and her control card is held or down: dealt nothing", name)
	}
	assert.Equal(t, 3, w.s.Fleet.Count(FriendRow("dan"), Ready)+w.s.Fleet.Count(FriendRow("dan"), Working), "a friend up whose control card is not held or down is dealt")
}

// A friend's configured work restriction (her nova-config row's streams and kinds,
// docs/SPEC-SPRINT.md section 1, a friend's card): the deal hands her no card outside it,
// on either the stream or the KIND, and an unrestricted friend is dealt as before.
func TestDealerNeverDealsAFriendOutsideHerStreams(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a", "reader-b")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
	brief := "c: in-scope friend work\nREPO: mas-bandwidth/nova-tools\nWHO: friend\nKIND: fix\n\nThe task."
	w.must(Add(w.s, AddReq{Stream: "security-a", Cards: []CardAdd{
		{ID: "security-a-1", Brief: brief},
		{ID: "security-a-2", Brief: strings.Replace(brief, "KIND: fix", "KIND: test", 1)},
	}}))
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{{ID: "s1-1", Brief: brief}}}))
	dealWith(w,
		FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash,pro", Streams: []string{"security*"}, Kinds: []string{"fix"}},
		FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"},
	)
	require.NotNil(t, w.s.Fleet.Card("security-a-1.w1"))
	assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card("security-a-1.w1").Row, "a matching stream and kind are hers")
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("security-a-2.w1").Row, "a KIND outside her restriction is dealt to the unrestricted friend")
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-1.w1").Row, "a stream outside her restriction is dealt to the unrestricted friend")
	assert.Equal(t, 1, w.s.Fleet.Count(FriendRow("amy"), Ready)+w.s.Fleet.Count(FriendRow("amy"), Working), "her row holds only the in-scope card")
}

func TestFriendRestrictionsTrimConfigWhitespace(t *testing.T) {
	t.Parallel()
	why := FriendRestrictionWhy(Split(" security* "), Split(" fix-red, review "), "security-a", "review")
	assert.Empty(t, why, "comma-separated config values with surrounding spaces match after sync")
}

func TestBriefKindReadsOnlyTheTypedHeader(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "fix-red", BriefKind("task\nREPO: mas-bandwidth/nova-tools\nKIND: fix-red\n\nThe work."))
	assert.Empty(t, BriefKind("task\nREPO: mas-bandwidth/nova-tools\n\nThe work.\nKIND: fix-red"), "a KIND line in the body does not grant a restriction match")
}

// FriendDeal is the tick's friend deal alone (friendDeal), its plan without the counts
// the level reads.
func FriendDeal(s *Snapshot, cards []*Card, seats []FriendSeat) Plan {
	p, _, _ := friendDeal(s, cards, seats)
	return p
}
