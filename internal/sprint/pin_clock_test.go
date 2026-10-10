package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A WHO pin is a preference with a clock (docs/SPEC-SPRINT.md, a friend's card).
// The world clock is the snapshot's Now. No socket and no time.Now.

func pinStory(p Plan) string {
	for _, u := range p.Units {
		if strings.Contains(u.Moved, "pin to ") {
			return u.Moved
		}
	}
	return ""
}

func TestPinWaivedWhenTheFriendIsDownPastTheBound(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	amy := FriendSeat{Name: "amy", Width: 2, Status: Down, Class: "flash,pro", Proof: w.s.Now.Add(-31 * time.Minute)}
	bob := FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"}
	p := dealWith(w, amy, bob)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Equal(t, FriendRow("bob"), wc.Row)
	pr := w.s.Primary("s1-1")
	assert.Equal(t, FriendRow("amy"), pr.F(FieldWho))
	assert.Equal(t, stamp(w.s.Now), pr.F(FieldPinWaived))
	assert.Contains(t, pinStory(p), "pin to amy waived after 31m0s: she is down; dealt to friend.bob")
}

func TestPinWaitsWhileTheFriendIsDownInsideTheBound(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	amy := FriendSeat{Name: "amy", Width: 2, Status: Down, Class: "flash,pro", Proof: w.s.Now.Add(-10 * time.Minute)}
	bob := FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"}
	p := dealWith(w, amy, bob)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Nil(t, w.s.Fleet.Card("s1-1.w1"))
	assert.Empty(t, w.s.Primary("s1-1").F(FieldPinWaived))
	assert.Empty(t, pinStory(p))

	// the bound itself still waits: longer than the pin wait is what waives
	w.s.Primary("s1-1").Fields[FieldPinSince] = stamp(w.s.Now.Add(-PinWaitDefault))
	amy.Proof = time.Time{}
	p = dealWith(w, amy, bob)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Empty(t, pinStory(p))
}

func TestPinOnlyNeverWaives(t *testing.T) {
	t.Parallel()
	brief := friendBrief("friend amy only")
	who, why := cardhdr.ReadWho(brief)
	require.Empty(t, why)
	assert.Equal(t, cardhdr.Who{Friend: true, Only: true, Name: "amy", Tail: true}, who)

	w := friendWorld(t, brief, friendBrief("only friend amy"))
	assert.Equal(t, []string{"s1-1"}, OnlyTailIDs(w.s))
	amy := FriendSeat{Name: "amy", Width: 2, Status: Down, Class: "flash,pro", Proof: w.s.Now.Add(-2 * time.Hour)}
	bob := FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"}
	p := dealWith(w, amy, bob)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Equal(t, Ready, w.s.StateOf("s1-2"))
	assert.Nil(t, w.s.Fleet.Card("s1-1.w1"))
	pr := w.s.Primary("s1-1")
	assert.Equal(t, "only."+FriendRow("amy"), pr.F(FieldWho))
	assert.Empty(t, pr.F(FieldPinWaived))
	assert.Empty(t, pinStory(p))
}

func TestPinPreferredFriendGetsTheNextAttemptFirst(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	amy := FriendSeat{Name: "amy", Width: 1, Status: Down, Class: "flash,pro", Proof: w.s.Now.Add(-31 * time.Minute)}
	bob := FriendSeat{Name: "bob", Width: 1, Status: Up, Class: "flash,pro"}
	cat := FriendSeat{Name: "cat", Width: 8, Status: Up, Class: "flash,pro"}
	p := dealWith(w, amy, bob, cat)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Equal(t, FriendRow("cat"), wc.Row)
	assert.Contains(t, pinStory(p), "pin to amy waived after 31m0s: she is down; dealt to friend.cat")
	assert.NotEmpty(t, w.s.Primary("s1-1").F(FieldPinWaived))

	w.must(FriendTake(w.s, FriendTakeReq{Friend: "cat", IDs: []string{"s1-1"}, Who: "rowan"}))
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Equal(t, FriendRow("amy"), w.s.Primary("s1-1").F(FieldWho))

	amy.Status, amy.Proof = Up, time.Time{}
	bob.Width = 8
	dealWith(w, amy, bob, cat)
	wc = w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Equal(t, FriendRow("amy"), wc.Row)
	assert.Empty(t, w.s.Primary("s1-1").F(FieldPinWaived))
	assert.Equal(t, FriendRow("amy"), w.s.Primary("s1-1").F(FieldWho))
}

func TestPinRebalanceReturnsOnlyAnUnstartedWaivedCard(t *testing.T) {
	t.Parallel()
	amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteFlash}}
	waived := stamp(t0.Add(-time.Hour))

	w := rbWorld(t, "m1", "m2")
	rbPlace(w, "s1-1", cardhdr.RouteFlash, "m1", Working)
	rbPlace(w, "s1-2", cardhdr.RouteFlash, "m1", Ready, "pr.who", FriendRow("amy"), "pr."+FieldPinWaived, waived)
	p := w.must(Rebalance(w.s, []FriendSeat{amy}, "machine"))
	require.Contains(t, strings.Join(rebalanced(p), "\n"), "rebalanced s1-2.w1 from m1 to friend.amy")
	assert.Empty(t, w.s.Primary("s1-2").F(FieldPinWaived))

	w = rbWorld(t, "m1", "m2")
	rbPlace(w, "s1-1", cardhdr.RouteFlash, "m1", Working)
	rbPlace(w, "s1-2", cardhdr.RouteFlash, "m1", Ready, "pr.who", FriendRow("amy"), "pr."+FieldPinWaived, waived, FieldProgress, waived)
	p = w.must(Rebalance(w.s, []FriendSeat{amy}, "machine"))
	assert.Empty(t, rebalanced(p))
	assert.Equal(t, "m1", w.s.Fleet.Card("s1-2.w1").Row)
	assert.Equal(t, waived, w.s.Primary("s1-2").F(FieldPinWaived))
}

func TestPinFullAndHeldUseTheClock(t *testing.T) {
	t.Parallel()
	fill := func(w *world, age time.Duration) {
		t.Helper()
		row := FriendRow("amy")
		if !w.s.Fleet.HasRow(row) {
			w.s.Fleet.SetRows(append(w.s.Fleet.Rows(), row))
		}
		for i, id := range []string{"fill-a", "fill-b"} {
			w.s.Fleet.Put(&Card{ID: id, Row: row, Col: Ready, Score: float64(i + 1), Rev: 1, Fields: map[string]string{
				"kind": "work", "dealt": stamp(w.s.Now.Add(-age)), "untaken_since": stamp(w.s.Now.Add(-age)),
			}})
		}
	}
	amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"}
	bob := FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"}

	w := friendWorld(t, friendBrief("friend amy"))
	fill(w, 31*time.Minute)
	w.s.Primary("s1-1").Fields[FieldPinSince] = stamp(w.s.Now.Add(-31 * time.Minute))
	p := dealWith(w, amy, bob)
	require.NotNil(t, w.s.Fleet.Card("s1-1.w1"))
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-1.w1").Row)
	assert.Contains(t, pinStory(p), "pin to amy waived after 31m0s: she is full; dealt to friend.bob")

	w = friendWorld(t, friendBrief("friend amy"))
	fill(w, 10*time.Minute)
	w.s.Primary("s1-1").Fields[FieldPinSince] = stamp(w.s.Now.Add(-10 * time.Minute))
	p = dealWith(w, amy, bob)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Empty(t, pinStory(p))

	w = friendWorld(t, friendBrief("friend amy"))
	amyHeld := amy
	amyHeld.Status = Held
	amyHeld.Since = w.s.Now.Add(-31 * time.Minute)
	p = dealWith(w, amyHeld, bob)
	assert.Contains(t, pinStory(p), "she is held; dealt to friend.bob")

	w = friendWorld(t, friendBrief("friend amy"))
	amyHeld.Since = w.s.Now.Add(-10 * time.Minute)
	p = dealWith(w, amyHeld, bob)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Empty(t, pinStory(p))
}

func TestPinFullStartsTheClockAndWaitsInsideTheBound(t *testing.T) {
	t.Parallel()
	fill := func(w *world) {
		t.Helper()
		row := FriendRow("amy")
		if !w.s.Fleet.HasRow(row) {
			w.s.Fleet.SetRows(append(w.s.Fleet.Rows(), row))
		}
		for i, id := range []string{"fill-a", "fill-b"} {
			w.s.Fleet.Put(&Card{ID: id, Row: row, Col: Ready, Score: float64(i + 1), Rev: 1, Fields: map[string]string{
				"kind": "work", "dealt": stamp(w.s.Now), "untaken_since": stamp(w.s.Now),
			}})
		}
	}
	amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"}
	bob := FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"}

	w := friendWorld(t, friendBrief("friend amy"))
	fill(w)

	// her full age is unknown on the first deal: the clock starts on the card and
	// keeps it for her this tick
	p := dealWith(w, amy, bob)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Nil(t, w.s.Fleet.Card("s1-1.w1"))
	assert.Equal(t, stamp(w.s.Now), w.s.Primary("s1-1").F(FieldPinSince), "the clock starts on the card")
	assert.Contains(t, pinWaitStory(p), "waits ready for friend amy (she is full)")

	// inside the bound from the started clock it still waits
	p = dealWith(w, amy, bob)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Empty(t, pinStory(p))

	// past the bound the started clock waives the pin: dealt on, WHO kept
	w.s.Now = w.s.Now.Add(PinWaitDefault + time.Minute)
	p = dealWith(w, amy, bob)
	require.NotNil(t, w.s.Fleet.Card("s1-1.w1"))
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-1.w1").Row)
	assert.Contains(t, pinStory(p), "pin to amy waived after 31m0s: she is full; dealt to friend.bob")
	assert.Equal(t, FriendRow("amy"), w.s.Primary("s1-1").F(FieldWho), "the WHO line stays as her preference")
}

func TestPinUnknownAbsenceWaivesAndAShortPinSinceHolds(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	amy := FriendSeat{Name: "amy", Width: 2, Status: Down, Class: "flash,pro"}
	bob := FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"}
	p := dealWith(w, amy, bob)
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-1.w1").Row)
	assert.Contains(t, pinStory(p), "pin to amy waived after 30m0s: she is down; dealt to friend.bob")

	w = friendWorld(t, friendBrief("friend amy"))
	w.s.Primary("s1-1").Fields[FieldPinSince] = stamp(w.s.Now.Add(-10 * time.Minute))
	amy.Proof = w.s.Now.Add(-2 * time.Hour)
	p = dealWith(w, amy, bob)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Empty(t, pinStory(p))
}

func TestPinWidthZeroAndARowFilledThisPassAreNotTheClock(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	amy := FriendSeat{Name: "amy", Width: 0, Status: Up, Class: "flash,pro"}
	bob := FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"}
	p := dealWith(w, amy, bob)
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-1.w1").Row)
	assert.Empty(t, w.s.Primary("s1-1").F(FieldPinWaived))
	assert.NotContains(t, pinStory(p), "waived")

	w = friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"))
	amy.Width = 1
	p = dealWith(w, amy, bob)
	assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-1.w1").Row)
	assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-2.w1").Row)
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-3.w1").Row)
	assert.Empty(t, w.s.Primary("s1-3").F(FieldPinWaived))
	assert.NotContains(t, pinStory(p), "waived")
}

func TestPinCountsAndThePreferredWord(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"))
	when := stamp(w.s.Now)
	a := w.s.Primary("s1-1")
	a.Fields[FieldPinWaived] = when
	b := w.s.Primary("s1-2")
	assert.Equal(t, PinCount{Pinned: 2, Waived: 1}, FriendPinCounts(w.s)["amy"])
	assert.Equal(t, "pinned 2, waived 1", PinCountWord(FriendPinCounts(w.s)["amy"]))
	assert.Equal(t, "preferred=amy waived="+when, PinPreferredWord(a))
	assert.Empty(t, PinPreferredWord(b))
	assert.Equal(t, PinWaitDefault, (&Snapshot{}).PinWait())
	w.s.Work.SetProp(PropPinWait, "5m")
	assert.Equal(t, 5*time.Minute, w.s.PinWait())
	w.must(Set(w.s, SetReq{PinWait: "45m", Who: w.s.Coordinator}))
	assert.Equal(t, 45*time.Minute, w.s.PinWait())
	w.must(Set(w.s, SetReq{PinWait: ReadTierDefault, Who: w.s.Coordinator}))
	assert.Equal(t, PinWaitDefault, w.s.PinWait())
}

func TestPinPreferredFriendIsAskedTheNextReadFirst(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	putReview(w, "s1-1", friendBrief("friend amy"), 1, 1, "h")
	pr := w.s.Primary("s1-1")
	pr.Fields[FieldWho] = FriendRow("amy")
	pr.Fields[FieldTierNow] = cardhdr.RouteFlash
	amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteFlash}}
	bob := FriendSeat{Name: "bob", Width: 8, Status: Up, Tiers: []string{cardhdr.RouteFlash}}
	p, waits, err := friendReadAsk(w.s, []FriendSeat{amy, bob}, "")
	require.NoError(t, err)
	require.Empty(t, waits)
	w.must(p)
	require.NotNil(t, w.s.Fleet.Card(ReadCardID("s1-1", 1, "amy")))
	assert.Nil(t, w.s.Fleet.Card(ReadCardID("s1-1", 1, "bob")))
}

// The hole the reader found (a-pin-is-a-preference-with-a-clock-bb-tb.w1): a WHO pin come
// back by a rework, a return or a redo is a preference with the clock too, never a hole.
// ReworkPinned is a soft pin: the deal starts its clock (pin_since), waits inside the
// bound, and waives it past the bound. TestAReworkKeepsTheWhoPin holds the other half:
// inside the clock she keeps it, and the machines' deal leaves it.

// pinWaitStory is the deal line that starts a come-back pin's clock, "" when no unit
// wrote one.
func pinWaitStory(p Plan) string {
	for _, u := range p.Units {
		if strings.Contains(u.Moved, "waits ready for friend ") {
			return u.Moved
		}
	}
	return ""
}

func TestAComeBackPinIsWaivedPastTheBound(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	w.s.Primary("s1-1").Fields["reworks"] = "1" // a rework, a return or a redo: ReworkPinned
	amy := FriendSeat{Name: "amy", Width: 2, Status: Down, Class: "flash,pro", Proof: w.s.Now.Add(-31 * time.Minute)}
	bob := FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"}
	p := dealWith(w, amy, bob)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc, "a come-back pin past the bound is dealt on, not a hole")
	assert.Equal(t, FriendRow("bob"), wc.Row)
	assert.Equal(t, stamp(w.s.Now), w.s.Primary("s1-1").F(FieldPinWaived))
	assert.Equal(t, FriendRow("amy"), w.s.Primary("s1-1").F(FieldWho), "the WHO line stays as her preference")
	assert.Contains(t, pinStory(p), "pin to amy waived after 31m0s: she is down; dealt to friend.bob")
}

func TestAComeBackPinStartsItsClockAndWaitsInsideTheBound(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	w.s.Primary("s1-1").Fields["reworks"] = "1"
	amy := FriendSeat{Name: "amy", Width: 2, Status: Down, Class: "flash,pro"}
	bob := FriendSeat{Name: "bob", Width: 2, Status: Up, Class: "flash,pro"}

	// her absence is unknown: the deal starts the clock and keeps the card this tick
	p := dealWith(w, amy, bob)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Nil(t, w.s.Fleet.Card("s1-1.w1"))
	assert.Equal(t, stamp(w.s.Now), w.s.Primary("s1-1").F(FieldPinSince), "the clock starts on the card")
	assert.Contains(t, pinWaitStory(p), "waits ready for friend amy")

	// inside the bound from the started clock it still waits
	p = dealWith(w, amy, bob)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Empty(t, pinStory(p))

	// past the bound the started clock waives the pin: dealt on, WHO kept
	w.s.Now = w.s.Now.Add(PinWaitDefault + time.Minute)
	p = dealWith(w, amy, bob)
	require.NotNil(t, w.s.Fleet.Card("s1-1.w1"))
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-1.w1").Row)
	assert.Contains(t, pinStory(p), "pin to amy waived after 31m0s: she is down; dealt to friend.bob")
	assert.Equal(t, FriendRow("amy"), w.s.Primary("s1-1").F(FieldWho), "the WHO line stays as her preference")
}
