package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestARouteIsUsedOnlyWhereItApplies is the route's applies mask
// (docs/SPEC-SPRINT.md, the deal; docs/SPEC-CONFIG.md, route): a work card is
// drawn only from the routes of its tier whose mask holds the executor it is
// dealt to, and a read only on a reader of a class its route's mask holds,
// never falling back to a route outside the mask. A route with applies=all is
// drawn by every class, as before the mask.
func TestARouteIsUsedOnlyWhereItApplies(t *testing.T) {
	t.Parallel()

	flashRoute := func(applies string) Route {
		r := Route{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "a", Tokens: 1000, Enabled: true, Applies: applies}
		r.Deadline = 600 // seconds, as the route row holds it
		return r
	}
	proRoute := func(applies string) Route {
		r := Route{Name: "pro-a", Tier: cardhdr.RoutePro, Provider: "p", Model: "a", Tokens: 1000, Enabled: true, Applies: applies}
		r.Deadline = 600 // seconds, as the route row holds it
		return r
	}
	snap := func(routes ...Route) *Snapshot {
		return &Snapshot{Now: t0, Fleet: NewTable(Fleet), Routes: routes,
			Tiers: map[string][]string{cardhdr.RouteFlash: {"flash-a"}, cardhdr.RoutePro: {"pro-a"}}}
	}
	pro := func() *Card {
		return &Card{ID: "s1-1", Fields: map[string]string{"brief": "tier: pro\n", FieldTierNow: cardhdr.RoutePro}}
	}
	flash := func() *Card {
		return &Card{ID: "s1-1", Fields: map[string]string{"brief": "tier: flash\n"}}
	}

	t.Run("a pro card is never dealt to a fleet member", func(t *testing.T) {
		t.Parallel()
		s := snap(proRoute(ClassFriends))
		_, _, why := s.routeOf(pro(), nil, ClassFleet, nil)
		require.NotEmpty(t, why, "a pro card is never dealt to a fleet member")
		assert.Contains(t, why, "applies", "the refusal names the mask: %s", why)
		set, _, why := s.routeOf(pro(), nil, ClassFriends, nil)
		require.Empty(t, why, "a pro card is dealt to a friend")
		assert.Equal(t, "pro-a", set[FieldRoute])
	})

	t.Run("a flash card is never dealt to a friend outside its mask", func(t *testing.T) {
		t.Parallel()
		s := snap(flashRoute(ClassFleet))
		_, _, why := s.routeOf(flash(), nil, ClassFriends, nil)
		require.NotEmpty(t, why, "a flash card is never dealt to a friend outside its mask")
		assert.Contains(t, why, "applies", "the refusal names the mask: %s", why)
		s = snap(flashRoute(ClassFriends))
		set, _, why := s.routeOf(flash(), nil, ClassFriends, nil)
		require.Empty(t, why, "a flash route that holds friends is dealt to a friend")
		assert.Equal(t, "flash-a", set[FieldRoute])
	})

	t.Run("a route with applies=all is drawn by every class", func(t *testing.T) {
		t.Parallel()
		s := snap(flashRoute(ClassAll))
		for _, class := range []string{ClassFleet, ClassFriends, ClassLocal} {
			set, _, why := s.routeOf(flash(), nil, class, nil)
			require.Empty(t, why, "applies=all is drawn by %s", class)
			assert.Equal(t, "flash-a", set[FieldRoute], "applies=all is drawn by %s", class)
		}
	})

	t.Run("a flash card is never dealt to a friend unless its route's mask holds friends", func(t *testing.T) {
		t.Parallel()
		w := friendWorld(t, "tier: flash\n\nThe task.")
		// the seed: flash-a is applied to the fleet, never to a friend, and amy's
		// tiers hold flash, so only the mask can keep the card off her row
		w.s.Routes = []Route{flashRoute(ClassFleet)}
		amy := FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash"}
		p := FriendDeal(w.s, []*Card{w.s.Primary("s1-1")}, []FriendSeat{amy})
		require.Empty(t, p.Refused)
		assert.Empty(t, p.Units, "no flash route applies to a friend: the friend deal leaves the card")
		assert.Nil(t, w.s.Fleet.Card("s1-1.w1"), "a flash card is never dealt to friend.amy while its route's mask holds fleet only")

		// the mask leaves it to the fleet: the machines' deal takes it
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
		wc := w.s.Fleet.Card("s1-1.w1")
		require.NotNil(t, wc, "the mask leaves the card to the fleet: the machines' deal takes it")
		assert.Contains(t, []string{"m1", "m2"}, wc.Row, "dealt to a fleet machine, never a friend")
		assert.Equal(t, "flash-a", wc.F(FieldRoute), "the fleet machine draws the masked route")
	})

	t.Run("an only-friend pin waits ready with the mask's reason", func(t *testing.T) {
		t.Parallel()
		w := friendWorld(t, friendBrief("only friend amy"))
		w.s.Routes = []Route{flashRoute(ClassFleet)}
		amy := FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash"}
		p := FriendDeal(w.s, []*Card{w.s.Primary("s1-1")}, []FriendSeat{amy})
		require.Empty(t, p.Refused)
		assert.Empty(t, p.Units, "the mask leaves the tier to the fleet: the pin waits ready")
		assert.Equal(t, Ready, w.s.StateOf("s1-1"), "the hard pin waits ready, never dealt to a machine")
		h := Holder(HeldState{Snap: w.s, Running: true}, w.s.Now, "s1-1")
		assert.Contains(t, h.Why, "no flash route applies to a friend", "where says the mask's reason: %s", h.Why)
	})

	t.Run("pro reads go only to friend readers", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-rowan", "reader-a")
		// reader-rowan is a friend's reader: its fleet row is the friend's
		w.s.Fleet.SetRows(append(w.s.Fleet.Rows(), FriendRow("rowan")))
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
		w.s.Routes = []Route{proRoute(ClassAll)}
		w.s.Tiers = map[string][]string{cardhdr.RoutePro: {"pro-a"}}
		w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Brief: proBrief}))
		w.s.Work.Card("s1-1").Fields[FieldTierNow] = cardhdr.RoutePro
		w.s.Work.Card("s1-1").Fields["head"] = "abc123"
		toReview(w, "s1-1")
		// the seed: every pro-* route runs on friends only
		w.s.Routes[0].Applies = ClassFriends
		pr := w.s.Work.Card("s1-1")
		assert.Equal(t, []string{"reader-rowan"}, w.s.freeReaders(pr, 1),
			"a pro read is placed on a friend reader, never a fleet reader")

		// no friend reader up: the read waits with the reason, on no route
		w.s.ReaderStates = map[string]string{"reader-rowan": ReaderAway, "reader-a": ReaderUp}
		assert.Empty(t, w.s.freeReaders(pr, 1), "no friend reader is up")
		p := Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}})
		require.NotEmpty(t, p.Refused, "a pro read with no friend reader up waits")
		assert.Contains(t, p.Refused[0].Why, "applies", "the reason names the mask: %s", p.Refused[0].Why)
	})
}
