package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// A frontier card goes to a friend up whose tiers hold frontier, exactly as a card of a tier
// friends alone serve does; it waits for the coordinator only while no friend up serves
// frontier, and a machine never draws it. On 2026-10-10 routeOf refused every frontier card
// before it asked the friends: `rework merge-tree-nodec` was refused "a frontier card waits
// for the coordinator" with stella up and frontier in her tiers, stranding a blocker, the
// rn-tla cards and the TLA+ cards.

// allTiers is a friend whose row lists every tier, as stella's does.
func allTiers(name, status string) FriendSeat {
	return FriendSeat{Name: name, Width: 2, Status: status,
		Tiers: []string{cardhdr.RouteFlash, cardhdr.RoutePro, cardhdr.RouteHeavy, cardhdr.RouteFrontier}}
}

// frontierRoutes is a store whose every tier has an enabled route, frontier's too: a
// frontier route on the fleet must still never be drawn for a frontier card.
func frontierRoutes() []Route {
	return []Route{
		{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "f", Enabled: true},
		{Name: "pro-a", Tier: cardhdr.RoutePro, Provider: "p", Model: "pr", Enabled: true},
		{Name: "heavy-a", Tier: cardhdr.RouteHeavy, Provider: "p", Model: "h", Enabled: true},
		{Name: "frontier-a", Tier: cardhdr.RouteFrontier, Provider: "p", Model: "fr", Enabled: true},
	}
}

// noMachineHolds says no machine's row holds a work card of the primary.
func noMachineHolds(t *testing.T, w *world, id string) {
	t.Helper()
	for _, m := range w.s.Members() {
		for _, col := range []State{Ready, Working} {
			for _, c := range w.s.Fleet.Cell(m, col) {
				assert.NotEqual(t, id, c.F("primary"), "machine %s holds a frontier card", m)
			}
		}
	}
}

func TestAFrontierCardIsDealtToAFrontierFriendUp(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, "c: model work tier: frontier\n\nThe task.")
	w.s.Routes = frontierRoutes()
	w.s.Work.SetProp(PropFleetTiers, "flash,pro,heavy")
	stella := allTiers("stella", Up)
	w.s.Friends = []FriendSeat{stella}

	set, tier, why, byFriend := w.s.routeOf(w.s.Primary("s1-1"), nil, nil)
	assert.Nil(t, set, "no machine route is drawn")
	assert.Equal(t, cardhdr.RouteFrontier, tier)
	assert.True(t, byFriend, "the friends' deal's: %s", why)
	assert.Contains(t, why, "stella")

	// the deal verb (a machine's deal) refuses it, saying the friends' deal deals it
	p := Deal(w.s, DealReq{})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "a frontier card is never a machine's; a friend up serves frontier (stella)")
	w.must(Plan{Units: p.Units})
	noMachineHolds(t, w, "s1-1")
	// the tick's deal hands it to her, and raises no judgment of the tier
	dealStarted(w, stella)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc, "the frontier card is dealt")
	assert.Equal(t, FriendRow("stella"), wc.Row)
	for _, o := range w.s.Open {
		assert.NotEqual(t, NNoRoute, o.Note.Type, "a frontier friend up serves it: %s", o.Note.What)
	}
}

func TestAFrontierCardIsNeverAMachinesWhateverTheFleetsTiers(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, "c: model work tier: frontier\n\nThe task.")
	w.s.Routes = frontierRoutes()
	w.s.Work.SetProp(PropFleetTiers, TiersAll) // the fleet's tiers hold frontier, and a frontier route is enabled
	set, _, why, byFriend := w.s.routeOf(w.s.Primary("s1-1"), nil, nil)
	assert.Nil(t, set, "the fleet's frontier route is never drawn for an unpinned frontier card")
	assert.False(t, byFriend)
	assert.Contains(t, why, "a frontier card waits for the coordinator")
	p := Deal(w.s, DealReq{})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "a frontier card waits for the coordinator: no friend up serves frontier (no friends)")
	w.must(Plan{Units: p.Units})
	noMachineHolds(t, w, "s1-1")
	assert.Equal(t, Ready, w.state("s1-1"))
}

func TestAFrontierCardWithNoFrontierFriendUpWaitsNamingWhoCould(t *testing.T) {
	t.Parallel()
	w := sideTierWorld(t)
	w.s.Friends = []FriendSeat{
		allTiers("stella", Held),
		{Name: "amy", Width: 2, Status: Up, Tiers: []string{cardhdr.RouteFlash, cardhdr.RoutePro}},
	}
	set, tier, why, byFriend := w.s.routeOf(sidePrimary("c: a card tier: frontier\n"), nil, nil)
	assert.Nil(t, set)
	assert.Equal(t, cardhdr.RouteFrontier, tier)
	assert.False(t, byFriend)
	assert.Contains(t, why, "a frontier card waits for the coordinator: no friend up serves frontier")
	assert.Contains(t, why, "stella (tiers flash,pro,heavy,frontier; held/down)")
	assert.Contains(t, why, "amy (tiers flash,pro; up, no frontier)")
	assert.Contains(t, why, "bring up a friend whose row lists frontier")

	w.s.Friends = nil
	_, _, why, _ = w.s.routeOf(sidePrimary("c: a card tier: frontier\n"), nil, nil)
	assert.Contains(t, why, "no friend up serves frontier (no friends)")

	// the friends' tiers leave frontier out: she is up with it, and still not dealt it
	w.s.Friends = []FriendSeat{allTiers("stella", Up)}
	w.s.Work.SetProp(PropFriendsTiers, "flash,pro,heavy")
	_, _, why, byFriend = w.s.routeOf(sidePrimary("c: a card tier: frontier\n"), nil, nil)
	assert.False(t, byFriend)
	assert.Contains(t, why, "stella (tiers flash,pro,heavy,frontier; up, --friends-tiers leaves out frontier)")
}

func TestReworkOfAFrontierCardGoesToAFrontierFriend(t *testing.T) {
	t.Parallel()
	frontierWorld := func(t *testing.T) *world {
		w := proOffWorld(t)
		w.s.Routes = frontierRoutes()
		w.s.Work.SetProp(PropFleetTiers, "flash,pro,heavy")
		return w
	}
	rework := func(w *world) Plan {
		return Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "model it", Tier: cardhdr.RouteFrontier})
	}
	t.Run("stella up with frontier: accepted, and dealt to her", func(t *testing.T) {
		t.Parallel()
		w := frontierWorld(t)
		stella := allTiers("stella", Up)
		w.s.Friends = []FriendSeat{stella}
		p := w.must(rework(w))
		require.Len(t, p.Units, 1)
		require.Empty(t, p.Refused)
		assert.Contains(t, p.Units[0].Moved, "review -> ready")
		assert.Contains(t, p.Units[0].Moved, "stella")
		assert.Equal(t, Ready, w.state("s1-1"))
		noMachineHolds(t, w, "s1-1")
		dealWith(w, stella)
		wc := w.s.Fleet.Card("s1-1.w2")
		require.NotNil(t, wc, "its next attempt is dealt")
		assert.Equal(t, FriendRow("stella"), wc.Row)
	})
	t.Run("no frontier friend up: refused naming the friends and their tiers", func(t *testing.T) {
		t.Parallel()
		w := frontierWorld(t)
		w.s.Friends = []FriendSeat{{Name: "johnny", Width: 2, Status: Up, Tiers: []string{cardhdr.RouteHeavy}}}
		p := rework(w)
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "a frontier card waits for the coordinator: no friend up serves frontier (friends: johnny (tiers heavy; up, no frontier))")
		assert.Equal(t, Review, w.state("s1-1"))
	})
}

// A frontier card's reads find a frontier-capable reader the same way: no side's tiers leave
// its read without a reader, and a friend is matched on frontier itself (friendReadTier), so
// a friend of frontier reads it and one whose tiers stop at heavy does not.
func TestAFrontierCardsReadFindsAFrontierReader(t *testing.T) {
	t.Parallel()
	w := sideTierWorld(t)
	w.s.Routes = frontierRoutes()
	w.s.Work.SetProp(PropFleetTiers, "flash,pro,heavy")
	stella := allTiers("stella", Up)
	johnny := FriendSeat{Name: "johnny", Width: 2, Status: Up, Tiers: []string{cardhdr.RouteHeavy}}
	w.s.Friends = []FriendSeat{stella, johnny}
	pr := sidePrimary("c: a card tier: frontier\n")
	_, why := w.s.readRouteMissing(pr)
	assert.Empty(t, why, "a frontier card's reads have a reader")
	assert.Equal(t, cardhdr.RouteFrontier, friendReadTier(w.s, pr))
	assert.True(t, friendAtOrAbove(w.s, stella, friendReadTier(w.s, pr)), "a frontier friend reads it")
	assert.False(t, friendAtOrAbove(w.s, johnny, friendReadTier(w.s, pr)), "a heavy friend does not")
}
