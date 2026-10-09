package sprint

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's card (docs/SPEC-SPRINT.md section 1; the owner, 2026-10-03: "doing parts on
// friends where we would normally do friend work"): a brief whose header says WHO: friend
// is dealt by the tick to a friend up below her width, on her own fleet row, straight into
// working; a card with no WHO line is the machines' as before.

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
	w := friendWorld(t, friendBrief("friend amy"))
	pr := w.s.Primary("s1-1")
	require.Equal(t, FriendRow("amy"), pr.F(FieldWho), "add writes the brief's WHO line on the card")

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
	w2 := friendWorld(t, friendBrief("friend"))
	p := Deal(w2.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "a friend's card")
}

func TestAFriendIsDealtHerRoomWorkingAtHerWidthAndReadyBehind(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend"))
	seats := []FriendSeat{{Name: "amy", Width: 2, Status: Up}, {Name: "bob", Width: 1, Status: Up}}
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
		briefs[i] = friendBrief("friend amy")
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
	dealWith(w, FriendSeat{Name: "amy", Width: 1, Status: Up}, FriendSeat{Name: "bob", Width: 2, Status: Up}, FriendSeat{Name: "cat", Width: 8, Status: Held})
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

// Dealing respects the friend row's delivery mode from nova-config: mode: batch|one-shot
// on the friend row (docs/SPEC-SPRINT.md section 1, "A friend's card"). In batch mode
// (the default) the machine deals up to the row's width
// as today (DealAhead times width, working at her width and ready behind); in one-shot mode
// it deals one card at a time and the next only after the last one finished.
func TestDealingRespectsAFriendDeliveryMode(t *testing.T) {
	t.Parallel()
	// Amy is batch (the default, width 2), Bob is one-shot (width 2, mode: one-shot).
	// Amy with 4 cards waiting gets 2 working and 2 ready behind.
	// Bob with 4 cards waiting gets 1 working and 0 ready; the other 3 wait on the work table.
	w := friendWorld(t,
		friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"),
		friendBrief("friend bob"), friendBrief("friend bob"), friendBrief("friend bob"), friendBrief("friend bob"),
	)
	seats := []FriendSeat{
		{Name: "amy", Width: 2, Status: Up, Mode: config.FriendModeBatch},
		{Name: "bob", Width: 2, Status: Up, Mode: config.FriendModeOneShot},
	}
	dealWith(w, seats...)

	amy := FriendRow("amy")
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Working), "amy works her width")
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Ready), "amy holds 2 ready behind (batch mode)")

	bob := FriendRow("bob")
	assert.Equal(t, 1, w.s.Fleet.Count(bob, Working), "bob gets exactly one card at a time in one-shot mode")
	assert.Equal(t, 0, w.s.Fleet.Count(bob, Ready), "bob holds no cards ready behind in one-shot mode")
	assert.Equal(t, Ready, w.s.StateOf("s1-6"), "the 2nd card for bob waits ready on the work table")
	assert.Equal(t, Ready, w.s.StateOf("s1-7"), "the 3rd card for bob waits ready on the work table")
	assert.Equal(t, Ready, w.s.StateOf("s1-8"), "the 4th card for bob waits ready on the work table")

	// A tick later without bob finishing: nothing more is dealt to bob
	dealWith(w, seats...)
	assert.Equal(t, 1, w.s.Fleet.Count(bob, Working))
	assert.Equal(t, 0, w.s.Fleet.Count(bob, Ready))
	assert.Equal(t, Ready, w.s.StateOf("s1-6"))

	// Bob finishes his first card: finish moves it out of working; at finish 0 are working
	// (no ready card auto-advances, unlike batch mode where friendNext takes the next)
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-5.w1"}}, As: bob, Gens: gensOf(w.s, "s1-5.w1"), Head: "abc"}))
	assert.Equal(t, 0, w.s.Fleet.Count(bob, Working), "bob has no ready card to auto-advance at finish")

	// The next deal/tick deals the next card to bob (and only one)
	dealWith(w, seats...)
	assert.Equal(t, 1, w.s.Fleet.Count(bob, Working), "the next card is dealt after the last one finished")
	assert.Equal(t, 0, w.s.Fleet.Count(bob, Ready))
	assert.Equal(t, Working, w.s.StateOf("s1-6"))
	assert.Equal(t, Ready, w.s.StateOf("s1-7"))

	// Bob finishes the 2nd card, next deal gives the 3rd
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-6.w1"}}, As: bob, Gens: gensOf(w.s, "s1-6.w1"), Head: "def"}))
	assert.Equal(t, 0, w.s.Fleet.Count(bob, Working))
	dealWith(w, seats...)
	assert.Equal(t, 1, w.s.Fleet.Count(bob, Working))
	assert.Equal(t, Working, w.s.StateOf("s1-7"))
	assert.Equal(t, Ready, w.s.StateOf("s1-8"))

	// WHO: friend (any friend) prefers friend with most room free:
	// Amy (batch, width 2, load 0 => free 4) vs Bob (one-shot, load 0 => free 1)
	w2 := friendWorld(t, friendBrief("friend"), friendBrief("friend"), friendBrief("friend"), friendBrief("friend"), friendBrief("friend"))
	dealWith(w2,
		FriendSeat{Name: "amy", Width: 2, Status: Up, Mode: config.FriendModeBatch},
		FriendSeat{Name: "bob", Width: 2, Status: Up, Mode: config.FriendModeOneShot},
	)
	assert.Equal(t, 2, w2.s.Fleet.Count(amy, Working), "amy gets 2 working")
	assert.Equal(t, 2, w2.s.Fleet.Count(amy, Ready), "amy gets 2 ready (fills her room 4)")
	assert.Equal(t, 1, w2.s.Fleet.Count(bob, Working), "5th card goes to bob who has free room 1")
	assert.Equal(t, 0, w2.s.Fleet.Count(bob, Ready), "bob holds none ready")
}

// In one-shot mode, friendNext gates promotion of ready cards until shared work/read occupancy
// on the friend's row reaches zero:
// 1. If another working card remains active on her row, finishing one card does not promote a ready card.
// 2. If an active frontier read card is on her row, finishing a work card does not promote a ready card.
func TestFriendNextGatesQueuedPromotionWhenActiveWorkOrReadRemainsInOneShot(t *testing.T) {
	t.Parallel()
	briefs := []string{
		friendBrief("friend amy"),
		friendBrief("friend amy"),
		friendBrief("friend amy"),
		friendBrief("friend amy"),
	}
	w := friendWorld(t, briefs...)
	// amy initially dealt in batch mode at width 2: s1-1 and s1-2 working, s1-3 and s1-4 ready
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Mode: config.FriendModeBatch})
	amy := FriendRow("amy")
	require.Equal(t, 2, w.s.Fleet.Count(amy, Working))
	require.Equal(t, 2, w.s.Fleet.Count(amy, Ready))

	// Amy switches to one-shot mode:
	seats := []FriendSeat{
		{Name: "amy", Width: 2, Status: Up, Mode: config.FriendModeOneShot},
	}

	// Finishing s1-1.w1 in one-shot mode: s1-2.w1 is still working, so occupancy is 1 > 0.
	// Ready cards (s1-3.w1, s1-4.w1) must not advance!
	w.must(Finish(w.s, FinishReq{
		Sel:     Sel{IDs: []string{"s1-1.w1"}},
		As:      amy,
		Gens:    gensOf(w.s, "s1-1.w1"),
		Head:    "abc",
		Friends: seats,
	}))
	assert.Equal(t, 1, w.s.Fleet.Count(amy, Working), "only s1-2.w1 remains working")
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Ready), "queued promotion is gated while s1-2 is active")
	assert.Equal(t, Working, w.s.Fleet.Card("s1-2.w1").Col)
	assert.Equal(t, Ready, w.s.Fleet.Card("s1-3.w1").Col)
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

	// Finish s1-2.w1: although the work card finished, the read card is still working!
	// Shared work/read occupancy is 1 > 0, so queued promotion remains gated.
	w.must(Finish(w.s, FinishReq{
		Sel:     Sel{IDs: []string{"s1-2.w1"}},
		As:      amy,
		Gens:    gensOf(w.s, "s1-2.w1"),
		Head:    "def",
		Friends: seats,
	}))
	assert.Equal(t, 1, w.s.Fleet.Count(amy, Working), "read-1 is still active")
	assert.Equal(t, 2, w.s.Fleet.Count(amy, Ready), "queued promotion gated by active read card")
	assert.Equal(t, Ready, w.s.Fleet.Card("s1-3.w1").Col)

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

// TestAFriendRowRedealtIsNotLeftOnTheOldFriend is the trace of FriendRedeal
// (tla/SprintEvents.tla, FriendNotLeft; friendRedealUnit). A card taken back
// from amy is placed again on bob. The snapshot clock is the only clock.
func TestAFriendRowRedealtIsNotLeftOnTheOldFriend(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"))
	w.s.Now = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	amy, bob := FriendRow("amy"), FriendRow("bob")
	seats := []FriendSeat{{Name: "amy", Width: 1, Status: Up}, {Name: "bob", Width: 1, Status: Up}}
	dealWith(w, seats...)
	require.Equal(t, amy, w.s.Fleet.Card("s1-1.w1").Row, "first by name, equal room")

	w.tick(time.Second)
	w.must(FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: []string{"s1-1"}, Reason: "she is on another job", Who: "rowan"}))
	taken := w.s.Fleet.Card("s1-1.w1")
	require.Equal(t, Withdrawn, taken.Col)
	require.Equal(t, amy, taken.Row, "taken back, still on her row")
	require.Equal(t, amy, taken.F(FieldTakenFrom))
	gen := taken.Int("gen")

	w.tick(time.Second)
	dealWith(w, seats...)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Equal(t, bob, wc.Row, "redealt onto the other friend")
	assert.NotEqual(t, amy, wc.Row, "a friend row that is redealt is not left on the old friend")
	assert.NotEqual(t, Withdrawn, wc.Col)
	assert.Empty(t, wc.F(FieldTakenFrom))
	assert.Greater(t, wc.Int("gen"), gen, "the same card, its next generation")
	assert.Equal(t, 0, w.s.Fleet.Count(amy, Ready)+w.s.Fleet.Count(amy, Working))
}
