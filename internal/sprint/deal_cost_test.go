package sprint

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// The deal by cost, whoever can work now first (the owner, 2026-10-10: "cards need to fill
// working slots FIRST across fleet and friends, then go to ready overflow up to 2X"; "the deal
// is lowest cost first"). tla/DealCost.tla is the model: NoQueuedWhileFreeSlot, ReadyCap,
// CheapestFirst and TierRespected are what these tests check of the code.

// costWorld is the machines given up at the width, a flash route, and stream s1.
func costWorld(t *testing.T, width int, members ...string) *world {
	t.Helper()
	w := newWorld(t)
	for _, m := range members {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m, Width: width}))
	}
	w.s.Routes = []Route{{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "f", Enabled: true}}
	w.s.Work.SetRows([]string{"s1"})
	return w
}

// pool adds n ready primaries of the tier in stream pool.
func pool(w *world, n int, tier string) {
	var cards []CardAdd
	for i := range n {
		cards = append(cards, CardAdd{ID: fmt.Sprintf("pool-%s-%d", tier, i+1), Brief: fleetBrief(tier)})
	}
	w.must(Add(w.s, AddReq{Stream: "pool", Cards: cards}))
}

// dealParts runs the deal's work-now part and its stack part on the world, each applied (the
// deal as it stood in one part before the rebalance came between them), and answers their plans
// together and the work-now part's due.
func (w *world) dealParts(r TickReq) (Plan, int) {
	w.t.Helper()
	p, due := TickDeal(w.s, r)
	p = w.do(p)
	q, _ := TickStack(w.s, r)
	q = w.do(q)
	return joinPlans(p, q), due
}

// joinPlans is the plans' rows, units, refusals, notes, props and updates, in order.
func joinPlans(p, q Plan) Plan {
	p.Rows, p.Units, p.Refused = append(p.Rows, q.Rows...), append(p.Units, q.Units...), append(p.Refused, q.Refused...)
	p.Notes, p.Props, p.Updates = append(p.Notes, q.Notes...), append(p.Props, q.Props...), append(p.Updates, q.Updates...)
	return p
}

// dealTick runs the deal's three parts in the pump's order: work now, rebalance, stack.
func dealTick(w *world, seats []FriendSeat) {
	w.t.Helper()
	r := TickReq{Friends: seats}
	w.part(TickDeal, r)
	w.part(TickRebalance, r)
	w.part(TickStack, r)
}

// readyWork is the work cards ready on the row.
func readyWork(w *world, row string) int {
	n := 0
	for _, c := range w.s.Fleet.Cell(row, Ready) {
		if c.F("kind") == "work" {
			n++
		}
	}
	return n
}

// freeLanes is the row's free lanes: its width less the work cards it holds.
func freeLanes(w *world, row string, width int) int { return width - workOn(w, row) }

func flashFriend(name string, width int) FriendSeat {
	return FriendSeat{Name: name, Width: width, Status: Up, Tiers: []string{cardhdr.RouteFlash}}
}

// The night of 2026-10-10, scaled down: Stella (width 4) at 3 working with 6 ready, Zhi (width
// 4) idle, two machines (width 2) idle, and 6 cards waiting in the pool. One tick fills every
// free lane, Zhi's first (a subscription friend is the cheapest), then the machines', and
// Stella's overflow goes to the lanes left; no row holds a card past its lanes while a lane
// that may take it is free.
func TestTheDealFillsEveryFreeLaneBeforeAnyStack(t *testing.T) {
	t.Parallel()
	w := costWorld(t, 2, "m1", "m2")
	stella, zhi := flashFriend("stella", 4), flashFriend("zhi", 4)
	for i := range 3 {
		wc := rbPlace(w, fmt.Sprintf("st-w%d", i+1), cardhdr.RouteFlash, FriendRow("stella"), Working)
		stella.Running = append(stella.Running, wc.ID) // her beat names her three started cards
	}
	for i := range 6 {
		rbPlace(w, fmt.Sprintf("st-r%d", i+1), cardhdr.RouteFlash, FriendRow("stella"), Ready)
	}
	pool(w, 6, cardhdr.RouteFlash)
	seats := []FriendSeat{stella, zhi}

	w.part(TickDeal, TickReq{Friends: seats})
	assert.GreaterOrEqual(t, workOn(w, FriendRow("zhi")), 4, "Zhi's free lanes first: she is the cheapest")
	assert.Equal(t, 2, workOn(w, "m1")+workOn(w, "m2"), "then the machines' free lanes take the pool's last two")
	for _, m := range []string{"m1", "m2"} {
		assert.LessOrEqual(t, workOn(w, m), 2, "the work-now part stacks no machine")
	}

	w.part(TickRebalance, TickReq{Friends: seats})
	w.part(TickStack, TickReq{Friends: seats})
	// no row has a free lane while any row holds a card past its lanes (NoQueuedWhileFreeSlot)
	for row, width := range map[string]int{FriendRow("zhi"): 4, FriendRow("stella"): 4, "m1": 2, "m2": 2} {
		assert.LessOrEqual(t, freeLanes(w, row, width), 0, "%s has a free lane while cards wait", row)
		assert.LessOrEqual(t, readyWork(w, row), DealAhead*width, "%s holds ready past its cap", row)
	}
	assert.Equal(t, 15, workOn(w, FriendRow("zhi"))+workOn(w, FriendRow("stella"))+workOn(w, "m1")+workOn(w, "m2"), "every card is on a row")
	assert.Equal(t, 3, w.s.Fleet.Count(FriendRow("stella"), Working), "a started card never moves")
	assert.Empty(t, w.s.Work.Column(Ready), "no card waits in the pool")
}

// A row's overflow moves to a free lane though a lane of its own is free (the old rebalance
// moved a card only off a row whose lanes all worked: the Stella row, 28 of 32 working and 50
// ready, never gave to Zhi at 0 of 32).
func TestTheOverflowMovesThoughALaneOfItsOwnIsFree(t *testing.T) {
	t.Parallel()
	w := costWorld(t, 1, "m1")
	for i := range 3 {
		rbPlace(w, fmt.Sprintf("st-w%d", i+1), cardhdr.RouteFlash, FriendRow("stella"), Working)
	}
	for i := range 5 {
		rbPlace(w, fmt.Sprintf("st-r%d", i+1), cardhdr.RouteFlash, FriendRow("stella"), Ready)
	}
	w.must(Rebalance(w.s, []FriendSeat{flashFriend("stella", 4), flashFriend("zhi", 4)}, "machine"))
	assert.Equal(t, 4, readyWork(w, FriendRow("zhi")), "her overflow, 4, to Zhi: a subscription friend before the machine")
	assert.Equal(t, 0, workOn(w, "m1"), "the overflow is gone before the machine is reached")
	assert.Equal(t, 1, readyWork(w, FriendRow("stella")), "she keeps the one her own free lane takes")
}

// A row's ready past its cap, twice its width, goes back to the pool, the newest first, when no
// free lane takes it; the deal's stack part deals it again within the rooms.
func TestTheReadyPastTwiceTheWidthReturnsToThePool(t *testing.T) {
	t.Parallel()
	w := costWorld(t, 1)
	rbPlace(w, "a-w", cardhdr.RouteFlash, FriendRow("amy"), Working)
	var ready []*Card
	for i := range 4 {
		ready = append(ready, rbPlace(w, fmt.Sprintf("a-r%d", i+1), cardhdr.RouteFlash, FriendRow("amy"), Ready))
	}
	amy := flashFriend("amy", 1)
	p := w.must(Rebalance(w.s, []FriendSeat{amy}, "machine"))
	assert.Equal(t, DealAhead*1, readyWork(w, FriendRow("amy")), "her ready is at her cap")
	for _, wc := range ready[2:] {
		assert.Equal(t, Withdrawn, w.s.Fleet.Card(wc.ID).Col, "%s, among the newest, is returned", wc.ID)
		assert.Equal(t, Ready, w.s.Work.Card(wc.F("primary")).Col, "its primary is ready for the deal")
	}
	for _, wc := range ready[:2] {
		assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card(wc.ID).Row, "%s, the oldest, stays", wc.ID)
	}
	notes := 0
	for _, u := range p.Units {
		for _, n := range u.Notes {
			if n.Type == NReturnedToPool {
				notes++
			}
		}
	}
	assert.Equal(t, 2, notes, "each return says so")
}

// A frontier card never goes to the fleet: no machine route serves it, so it waits for a friend
// whose tiers hold it, stacked on her row while her lanes work, and the rebalance never moves
// it to an idle machine.
func TestAFrontierCardNeverGoesToTheFleet(t *testing.T) {
	t.Parallel()
	w := costWorld(t, 2, "m1")
	fran := FriendSeat{Name: "fran", Width: 2, Status: Up, Tiers: []string{cardhdr.RouteFlash, cardhdr.RouteFrontier}}
	rbPlace(w, "f-w1", cardhdr.RouteFrontier, FriendRow("fran"), Working)
	rbPlace(w, "f-w2", cardhdr.RouteFrontier, FriendRow("fran"), Working)
	queued := rbPlace(w, "f-r", cardhdr.RouteFrontier, FriendRow("fran"), Ready)
	pool(w, 1, cardhdr.RouteFrontier)
	dealTick(w, []FriendSeat{fran})
	assert.Equal(t, 0, workOn(w, "m1"), "the idle machine is dealt no frontier card")
	assert.Equal(t, FriendRow("fran"), w.s.Fleet.Card(queued.ID).Row, "her queued frontier card stays with her")
	assert.Equal(t, 4, workOn(w, FriendRow("fran")), "the pool's frontier card is stacked on her row, within her room")
}

// The deal is lowest cost first: a full subscription friend and an idle machine, the card goes
// to the machine; an idle subscription friend and an idle machine, to the friend; an idle
// API-rate friend and an idle machine, to the machine, and the API-rate friend takes a card
// only into a free lane, after the machines' lanes are full, never a stack.
func TestTheDealIsLowestCostFirst(t *testing.T) {
	t.Parallel()
	t.Run("full friend, idle machine", func(t *testing.T) {
		t.Parallel()
		w := costWorld(t, 1, "m1")
		rbPlace(w, "a-w", cardhdr.RouteFlash, FriendRow("amy"), Working)
		pool(w, 1, cardhdr.RouteFlash)
		w.part(TickDeal, TickReq{Friends: []FriendSeat{flashFriend("amy", 1)}})
		assert.Equal(t, 1, workOn(w, "m1"), "the machine can work it now")
		assert.Equal(t, 1, workOn(w, FriendRow("amy")), "nothing stacked on the full friend")
	})
	t.Run("idle friend, idle machine", func(t *testing.T) {
		t.Parallel()
		w := costWorld(t, 1, "m1")
		pool(w, 1, cardhdr.RouteFlash)
		dealTick(w, []FriendSeat{flashFriend("amy", 1)})
		assert.Equal(t, 1, workOn(w, FriendRow("amy")), "a subscription friend costs nothing more")
		assert.Equal(t, 0, workOn(w, "m1"))
	})
	t.Run("API-rate friend", func(t *testing.T) {
		t.Parallel()
		w := costWorld(t, 1, "m1")
		alex := flashFriend("alex", 1)
		alex.Billing = "api"
		pool(w, 4, cardhdr.RouteFlash)
		w.part(TickDeal, TickReq{Friends: []FriendSeat{alex}})
		assert.Equal(t, 1, workOn(w, "m1"), "the machine's free lane first")
		assert.Equal(t, 0, workOn(w, FriendRow("alex")), "an API-rate friend is dearer than the fleet")
		w.part(TickRebalance, TickReq{Friends: []FriendSeat{alex}})
		w.part(TickStack, TickReq{Friends: []FriendSeat{alex}})
		assert.Equal(t, 1, workOn(w, FriendRow("alex")), "her free lane before any stack, and never a stack of her own")
		assert.Equal(t, DealAhead*1, workOn(w, "m1"), "the machine is stacked to its room")
	})
}

// A friend down or held keeps no stack: the rebalance returns her unstarted cards to the pool;
// one she started stays.
func TestAFriendDownKeepsNoStack(t *testing.T) {
	t.Parallel()
	w := costWorld(t, 1)
	started := rbPlace(w, "d-s", cardhdr.RouteFlash, FriendRow("dee"), Ready)
	queued := rbPlace(w, "d-r", cardhdr.RouteFlash, FriendRow("dee"), Ready)
	dee := flashFriend("dee", 2)
	dee.Status = Down
	dee.Running = []string{started.ID}
	w.must(Rebalance(w.s, []FriendSeat{dee}, "machine"))
	assert.Equal(t, Withdrawn, w.s.Fleet.Card(queued.ID).Col, "her unstarted card goes back to the pool")
	assert.Equal(t, Ready, w.s.Work.Card("d-r").Col)
	require.NotNil(t, w.s.Fleet.Card(started.ID))
	assert.Equal(t, FriendRow("dee"), w.s.Fleet.Card(started.ID).Row, "the card her lane runs stays")
}
