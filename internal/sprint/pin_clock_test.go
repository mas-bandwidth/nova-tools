package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A pin is a preference with a clock, never a hole (the owner, 2026-10-07: 58 ready cards
// pinned to friends down or out of credits sat for hours while the fleet idled; friend_deal.go,
// a pin is a preference with a clock). A card come back to the friend its WHO line names
// (ReworkPinned) waits ready for her while she is down, held or full up to the pin bound
// (set --pin-wait, PinWaitDefault), then its pin is waived: dealt as unpinned to the next unit
// of its tier with one story line, the WHO line kept as her preference. WHO: friend <name>
// only is never waived. She gets the next attempt first when she is back with room, and the
// rebalance gives an unstarted waived card back to her the tick she is up with an idle lane.

// pinAmy is amy's seat at the status given, of the card's tier.
func pinAmy(status string) FriendSeat {
	return FriendSeat{Name: "amy", Width: 2, Status: status, Class: "flash,pro"}
}

// pinBob is a second friend of the same tier, up with room.
var pinBob = FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"}

// comeBackToAmy is a world whose card s1-1 says WHO: friend amy, was dealt to her, started,
// failed and reworked: a card come back to her (ReworkPinned), ready for its second attempt.
func comeBackToAmy(t *testing.T, who string) *world {
	t.Helper()
	w := friendWorld(t, friendBrief(who))
	dealStarted(w, pinAmy(Up))
	require.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-1.w1").Row, "its first deal is hers")
	require.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col, "she started it")
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: "HOLD: the gate is red"}))
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "make the gate green"}))
	require.Equal(t, Ready, w.s.StateOf("s1-1"))
	require.True(t, OnlyFriend(w.s.Primary("s1-1")), "come back by a rework, or the hard pin: hers alone")
	return w
}

// waivedNotes is the plan's pin-waived story lines.
func waivedNotes(p Plan) []string {
	var out []string
	for _, u := range p.Units {
		for _, n := range u.Notes {
			if n.Type == NPinWaived {
				out = append(out, n.What)
			}
		}
	}
	return out
}

// Under the bound the card waits for her, the clock started once; at the bound its pin is
// waived and it is dealt to a friend of its tier with the story line, the WHO line kept.
func TestAPinnedCardWaitsForHerThenIsWaivedAtTheBound(t *testing.T) {
	t.Parallel()
	w := comeBackToAmy(t, "friend amy")
	pr := w.s.Primary("s1-1")
	require.Empty(t, pr.F(FieldPinWaitSince))

	// amy down, bob up with room: under the bound it waits for her, and the clock starts
	p := dealWith(w, pinAmy(Down), pinBob)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Nil(t, w.s.Fleet.Card("s1-1.w2"), "no next attempt while she is down under the bound")
	assert.Empty(t, w.s.Fleet.Cell(FriendRow("bob"), Ready), "bob is dealt nothing")
	assert.Empty(t, waivedNotes(p))
	since := w.s.Primary("s1-1").F(FieldPinWaitSince)
	assert.Equal(t, stamp(w.s.Now), since, "the clock starts on the first tick she cannot take it")
	require.Len(t, p.Units, 1)
	assert.Contains(t, p.Units[0].Moved, "s1-1 waits ready for friend amy (she is down): its pin is waived after 30m0s")
	// the machines' deal verb leaves it to her
	d := Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}})
	require.Len(t, d.Refused, 1)
	assert.Contains(t, d.Refused[0].Why, "a friend's card")

	// a minute under the bound, held now: still hers, the clock written once
	w.tick(PinWaitDefault - time.Minute)
	p = dealWith(w, pinAmy(Held), pinBob)
	assert.Nil(t, w.s.Fleet.Card("s1-1.w2"), "under the bound it waits")
	assert.Equal(t, since, w.s.Primary("s1-1").F(FieldPinWaitSince), "the clock is not restarted")
	assert.Empty(t, p.Units, "nothing to write: the clock runs")

	// at the bound: waived, dealt to bob, one story line, the WHO line kept
	w.tick(time.Minute)
	p = dealWith(w, pinAmy(Down), pinBob)
	wc := w.s.Fleet.Card("s1-1.w2")
	require.NotNil(t, wc, "at the bound its next attempt is dealt")
	assert.Equal(t, FriendRow("bob"), wc.Row, "to the next unit of its tier")
	assert.Equal(t, Ready, wc.Col)
	pr = w.s.Primary("s1-1")
	assert.Equal(t, Working, pr.Col)
	assert.Equal(t, FriendRow("amy"), pr.F(FieldWho), "the WHO line stays: her preference")
	assert.Equal(t, stamp(w.s.Now), pr.F(FieldPinWaived))
	assert.Empty(t, pr.F(FieldPinWaitSince), "the clock comes off with the deal")
	assert.Equal(t, []string{"pin to amy waived after 30m0s: she is down; dealt to friend.bob"}, waivedNotes(p))
	assert.False(t, OnlyFriend(pr), "waived: dealt as unpinned until she takes it")
	// no pin-ignored judgment from the deal: the story line is the one line
	for _, u := range p.Units {
		for _, n := range u.Notes {
			assert.NotEqual(t, NPinIgnored, n.Type)
		}
	}
}

// Full is a reason with a clock too: she is up but at her room; and a pin she can never
// honour (the card has left her) is waived at once.
func TestAFullFriendRunsThePinClockAndALeftPinIsWaivedAtOnce(t *testing.T) {
	t.Parallel()
	t.Run("full", func(t *testing.T) {
		t.Parallel()
		w := comeBackToAmy(t, "friend amy")
		full := FriendSeat{Name: "amy", Width: 0, Status: Up, Class: "flash,pro"}
		dealWith(w, full, pinBob)
		assert.Nil(t, w.s.Fleet.Card("s1-1.w2"), "she is full: it waits for her under the bound")
		assert.NotEmpty(t, w.s.Primary("s1-1").F(FieldPinWaitSince))
		w.tick(PinWaitDefault)
		p := dealWith(w, full, pinBob)
		require.NotNil(t, w.s.Fleet.Card("s1-1.w2"))
		assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-1.w2").Row)
		assert.Equal(t, []string{"pin to amy waived after 30m0s: she is full; dealt to friend.bob"}, waivedNotes(p))
	})
	t.Run("left her", func(t *testing.T) {
		t.Parallel()
		w := comeBackToAmy(t, "friend amy")
		dealWith(w, pinAmy(Up))
		wc := w.s.Fleet.Card("s1-1.w2")
		require.NotNil(t, wc)
		require.Equal(t, FriendRow("amy"), wc.Row, "she is up: hers first")
		// the coordinator takes it back from her: it has left her, and waits for no clock
		w.must(FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: []string{wc.ID}, Reason: "unstarted", Who: "coordinator"}))
		require.Equal(t, Ready, w.s.StateOf("s1-1"))
		p := dealWith(w, pinAmy(Up), pinBob)
		got := w.s.Fleet.Card("s1-1.w2")
		require.NotNil(t, got)
		assert.Equal(t, FriendRow("bob"), got.Row, "dealt on at once: she cannot take it back")
		assert.Equal(t, []string{"pin to amy waived: it has left her; dealt to friend.bob"}, waivedNotes(p))
	})
}

// With no friend of its tier the waived card goes to the fleet, the machines' deal writing
// the story line.
func TestAWaivedPinFallsThroughToTheFleet(t *testing.T) {
	t.Parallel()
	w := comeBackToAmy(t, "friend amy")
	dealWith(w, pinAmy(Down))
	assert.Nil(t, w.s.Fleet.Card("s1-1.w2"), "under the bound: no machine is dealt it")
	w.tick(PinWaitDefault)
	p := dealWith(w, pinAmy(Down))
	wc := w.s.Fleet.Card("s1-1.w2")
	require.NotNil(t, wc)
	assert.Contains(t, []string{"m1", "m2"}, wc.Row, "a machine of its tier")
	pr := w.s.Primary("s1-1")
	assert.Equal(t, FriendRow("amy"), pr.F(FieldWho))
	assert.Equal(t, stamp(w.s.Now), pr.F(FieldPinWaived))
	assert.Empty(t, pr.F(FieldPinWaitSince))
	assert.Equal(t, []string{"pin to amy waived after 30m0s: she is down; dealt to " + wc.Row}, waivedNotes(p))
}

// The hard pin, WHO: friend <name> only (and the older WHO: only friend <name>), is never
// waived: past the bound it still waits for her, no clock written.
func TestAnOnlyPinIsNeverWaived(t *testing.T) {
	t.Parallel()
	for _, who := range []string{"friend amy only", "only friend amy"} {
		t.Run(who, func(t *testing.T) {
			t.Parallel()
			w := comeBackToAmy(t, who)
			require.Equal(t, "only."+FriendRow("amy"), w.s.Primary("s1-1").F(FieldWho))
			require.True(t, HardPin(w.s.Primary("s1-1")))
			for range 3 {
				p := dealWith(w, pinAmy(Down), pinBob)
				assert.Nil(t, w.s.Fleet.Card("s1-1.w2"), "it waits for her alone")
				assert.Empty(t, w.s.Fleet.Cell(FriendRow("bob"), Ready))
				assert.Empty(t, waivedNotes(p))
				assert.Empty(t, w.s.Primary("s1-1").F(FieldPinWaitSince), "no clock on a hard pin")
				w.tick(2 * PinWaitDefault)
			}
			assert.Contains(t, Holder(running(w), w.s.Now, "s1-1").Why, "waits for only friend amy")
			dealWith(w, pinAmy(Up), pinBob)
			require.NotNil(t, w.s.Fleet.Card("s1-1.w2"))
			assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-1.w2").Row, "up: hers")
		})
	}
}

// The preferred friend gets the next attempt first when she is up with room: the deal onto
// her row clears the waiver; while she is still away the waived card flows on at once.
func TestThePreferredFriendGetsTheNextAttemptFirst(t *testing.T) {
	t.Parallel()
	waivedToBob := func(t *testing.T) *world {
		t.Helper()
		w := comeBackToAmy(t, "friend amy")
		dealWith(w, pinAmy(Down), pinBob)
		w.tick(PinWaitDefault)
		dealWith(w, pinAmy(Down), pinBob)
		require.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-1.w2").Row)
		// bob starts it, fails it, and it is reworked: ready for its third attempt
		startLanes(w, pinBob)
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w2"}}, As: FriendRow("bob"), Gens: gensOf(w.s, "s1-1.w2"), Failed: true, Report: "HOLD: red again"}))
		w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "another fix"}))
		require.Equal(t, Ready, w.s.StateOf("s1-1"))
		require.NotEmpty(t, w.s.Primary("s1-1").F(FieldPinWaived), "the waiver stands until she takes it")
		return w
	}
	t.Run("she is back", func(t *testing.T) {
		t.Parallel()
		w := waivedToBob(t)
		dealWith(w, pinAmy(Up), pinBob)
		wc := w.s.Fleet.Card("s1-1.w3")
		require.NotNil(t, wc)
		assert.Equal(t, FriendRow("amy"), wc.Row, "her preference: the next attempt is hers first")
		pr := w.s.Primary("s1-1")
		assert.Empty(t, pr.F(FieldPinWaived), "a deal onto her row clears the waiver")
		assert.Empty(t, pr.F(FieldPinWaitSince))
		assert.True(t, OnlyFriend(pr), "come back to her again: hers within the bound")
	})
	t.Run("she is still away", func(t *testing.T) {
		t.Parallel()
		w := waivedToBob(t)
		p := dealWith(w, pinAmy(Down), pinBob)
		wc := w.s.Fleet.Card("s1-1.w3")
		require.NotNil(t, wc, "a waived pin flows on at once")
		assert.Equal(t, FriendRow("bob"), wc.Row)
		assert.Empty(t, waivedNotes(p), "waived once, told once")
	})
}

// The rebalance gives an unstarted waived card back to her the tick she is up with an idle
// lane, the waiver cleared; a card bob started stays his.
func TestTheRebalanceMovesOnlyUnstartedWaivedCardsBack(t *testing.T) {
	t.Parallel()
	waived := func(t *testing.T) *world {
		t.Helper()
		w := comeBackToAmy(t, "friend amy")
		dealWith(w, pinAmy(Down), pinBob)
		w.tick(PinWaitDefault)
		dealWith(w, pinAmy(Down), pinBob)
		require.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-1.w2").Row)
		return w
	}
	t.Run("unstarted: back to her", func(t *testing.T) {
		t.Parallel()
		w := waived(t)
		p := w.must(Rebalance(w.s, []FriendSeat{pinAmy(Up), pinBob}, "machine"))
		require.Len(t, p.Units, 1)
		wc := w.s.Fleet.Card("s1-1.w2")
		assert.Equal(t, FriendRow("amy"), wc.Row)
		assert.Equal(t, Ready, wc.Col, "ready on her row until she starts it")
		assert.Equal(t, "2", wc.F("gen"), "its next generation: bob's inbox job is stale")
		assert.Equal(t, "2", wc.F("attempt"), "its attempt kept")
		assert.Equal(t, FriendRow("bob"), wc.F(FieldRebalancedFrom))
		pr := w.s.Primary("s1-1")
		assert.Empty(t, pr.F(FieldPinWaived), "her preference honoured: the waiver comes off")
		assert.Equal(t, FriendRow("amy"), pr.F(FieldWho))
		assert.Contains(t, p.Units[0].Moved, "rebalanced s1-1.w2 from friend.bob back to friend.amy gen=2 (its pin to amy, waived at ")
		require.Len(t, p.Units[0].Notes, 1)
		assert.Equal(t, NRebalanced, p.Units[0].Notes[0].Type)
		assert.Contains(t, p.Units[0].Notes[0].What, "is honoured: she is up with an idle lane")
		// and not again: it sits on her row
		assert.Empty(t, Rebalance(w.s, []FriendSeat{pinAmy(Up), pinBob}, "machine").Units)
	})
	t.Run("started: stays", func(t *testing.T) {
		t.Parallel()
		w := waived(t)
		startLanes(w, pinBob)
		require.Equal(t, Working, w.s.Fleet.Card("s1-1.w2").Col, "bob started it")
		p := w.must(Rebalance(w.s, []FriendSeat{pinAmy(Up), pinBob}, "machine"))
		assert.Empty(t, p.Units, "a started card is his to finish")
		assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-1.w2").Row)
		assert.NotEmpty(t, w.s.Primary("s1-1").F(FieldPinWaived))
	})
	t.Run("she is down: stays", func(t *testing.T) {
		t.Parallel()
		w := waived(t)
		assert.Empty(t, Rebalance(w.s, []FriendSeat{pinAmy(Down), pinBob}, "machine").Units)
	})
}

// PinCounts is each friend's pins over the ready and working primaries: pinned, waived, only.
func TestPinCountsPerFriend(t *testing.T) {
	t.Parallel()
	w := comeBackToAmy(t, "friend amy")
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{{ID: "s1-2", Brief: friendBrief("friend amy only")}, {ID: "s1-3", Brief: friendBrief("friend bob")}, {ID: "s1-4", Brief: "c: the fleet's\nREPO: mas-bandwidth/nova-tools\n\nThe task."}}}))
	assert.Equal(t, map[string]PinCount{"amy": {Pinned: 2, Only: 1}, "bob": {Pinned: 1}}, PinCounts(w.s.Work))
	dealWith(w, pinAmy(Down), pinBob)
	w.tick(PinWaitDefault)
	dealWith(w, pinAmy(Down), pinBob)
	require.NotEmpty(t, w.s.Primary("s1-1").F(FieldPinWaived))
	assert.Equal(t, map[string]PinCount{"amy": {Pinned: 2, Waived: 1, Only: 1}, "bob": {Pinned: 1}}, PinCounts(w.s.Work))
	assert.Empty(t, PinCounts(nil))
}
