package sprint

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The route's lane cap (docs/SPEC-SPRINT.md, the deal; internal/config/kind.go, "route"):
// the most lanes in flight on one route at once, so a key shared across a tier's routes is
// never overrun. A lane is a card or a read started on the route and not finished. The deal
// skips a route at its cap and takes the next of the tier; when every route of a tier is at
// its cap the card waits and the tier's one judgment says so ("every route at its lane
// cap"). A provider rate limit halves the cap for ten minutes, then restores.

// laneWorld is two flash routes with a lane cap of one each, dealing from the store's
// tier array: flash-a then flash-b.
func laneWorld() *Snapshot {
	s := &Snapshot{
		Now:   t0,
		Fleet: NewTable(Fleet),
		Routes: []Route{
			{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "a", Enabled: true, Lanes: 1},
			{Name: "flash-b", Tier: cardhdr.RouteFlash, Provider: "q", Model: "b", Enabled: true, Lanes: 1},
		},
		Tiers: map[string][]string{cardhdr.RouteFlash: {"flash-a", "flash-b"}},
	}
	// Column reads a card through its row (Table.Column), so the rows the lanes sit on
	// are the table's: a machine's and a friend's
	s.Fleet.SetRows([]string{"m1", FriendRow("amy")})
	return s
}

// laneCard is a fresh flash primary, as the deal reads one.
func laneCard(id string) *Card {
	return &Card{ID: id, Fields: map[string]string{"brief": "tier: flash\n"}}
}

// laneTake is one ended take on a route, as the fleet table records it: the provider's
// line, at the clock given.
func laneTake(route, line string, at time.Time) *Card {
	return &Card{ID: "old-" + route, Row: "m1", Col: DoneFailed, Fields: map[string]string{
		"ok": "no", FieldRoute: route, "finished": stamp(at),
		FieldProviderTake + "1": ProviderTake{Route: route, Model: "p/" + route, Member: "m1", Finished: stamp(at), Error: line}.String(),
	}}
}

// TestARouteAtItsLaneCapIsSkippedForTheNext: a route with a lane in flight on it draws no
// second lane while its cap is one, so the deal takes the next route of the tier.
func TestARouteAtItsLaneCapIsSkippedForTheNext(t *testing.T) {
	t.Parallel()
	s := laneWorld()
	s.Fleet.Put(&Card{ID: "w1", Row: "m1", Col: Working, Fields: map[string]string{FieldRoute: "flash-a"}})
	s = s.withLanePlan()

	set, tier, why, _ := s.routeOf(laneCard("s1-1"), nil, nil)
	require.Empty(t, why)
	assert.Equal(t, cardhdr.RouteFlash, tier)
	assert.Equal(t, "flash-b", set[FieldRoute], "a route at its lane cap is skipped for the next")
	assert.Equal(t, "q/b", set[FieldModel], "the next route's own model rides with it")

	// the cap is a count, not a disable: one lane in flight under a cap of two leaves the
	// route drawable
	wide := laneWorld()
	wide.Fleet.Put(&Card{ID: "w1", Row: "m1", Col: Working, Fields: map[string]string{FieldRoute: "flash-a"}})
	wide.Routes[0].Lanes = 2
	wide = wide.withLanePlan()
	set, _, why, _ = wide.routeOf(laneCard("s1-2"), nil, nil)
	require.Empty(t, why)
	assert.Equal(t, "flash-a", set[FieldRoute], "one lane in flight is under a cap of two")
}

// TestEveryRouteAtItsLaneCapWaitsWithTheLine: with every route of the tier at its cap the
// card is not dealt, and the refusal is the deal's own line, once for the tier.
func TestEveryRouteAtItsLaneCapWaitsWithTheLine(t *testing.T) {
	t.Parallel()
	s := laneWorld()
	s.Fleet.Put(&Card{ID: "w1", Row: "m1", Col: Working, Fields: map[string]string{FieldRoute: "flash-a"}})
	s.Fleet.Put(&Card{ID: "w2", Row: "m1", Col: Working, Fields: map[string]string{FieldRoute: "flash-b"}})
	s = s.withLanePlan()

	set, tier, why, _ := s.routeOf(laneCard("s1-1"), nil, nil)
	assert.Nil(t, set, "a card whose tier is at its lane cap is not dealt")
	assert.Equal(t, cardhdr.RouteFlash, tier)
	assert.Contains(t, why, NRouteLaneCap, "the deal says every route is at its lane cap")
	assert.Contains(t, why, cardhdr.RouteFlash)
}

// TestAFriendsLaneOnTheRouteCounts: a lane on a friend's row that names the route counts
// against its cap, as a machine's does: her working card and her read card in flight.
func TestAFriendsLaneOnTheRouteCounts(t *testing.T) {
	t.Parallel()
	s := laneWorld()
	s.Fleet.Put(&Card{ID: "f1", Row: FriendRow("amy"), Col: Working, Fields: map[string]string{FieldRoute: "flash-a"}})
	assert.Equal(t, 1, routeLanes(s.Fleet, "flash-a"), "a friend's working card counts as a lane")
	assert.Equal(t, 0, routeLanes(s.Fleet, "flash-b"))

	// her read in flight on the route counts too
	s.Fleet.Put(&Card{ID: "r1", Row: FriendRow("amy"), Col: Asked, Fields: map[string]string{"kind": "read", FieldRoute: "flash-a"}})
	assert.Equal(t, 2, routeLanes(s.Fleet, "flash-a"), "a friend's read card counts as a lane")

	// and the deal skips the route for it: two lanes fill a cap of two
	s.Routes[0].Lanes = 2
	s = s.withLanePlan()
	set, _, why, _ := s.routeOf(laneCard("s1-1"), nil, nil)
	require.Empty(t, why)
	assert.Equal(t, "flash-b", set[FieldRoute], "her lanes fill the route's cap")

	// a read the deal would draw takes the next route, never one at its cap
	pr := &Card{ID: "s1-2", Fields: map[string]string{"brief": "tier: flash\n", FieldTierNow: cardhdr.RouteFlash}}
	fields := s.readRouteOf(routeIndexesOf(s), pr, nil)
	assert.Equal(t, "flash-b", fields[FieldRoute], "the read takes the next route of its tier")
}

// TestARateLimitHalvesTheLaneCapForTheWindowAndRestores: a provider rate limit on a route
// halves its effective lane cap for RouteLaneRateWindow from the take, then restores; the
// route is never disabled for it.
func TestARateLimitHalvesTheLaneCapForTheWindowAndRestores(t *testing.T) {
	t.Parallel()
	s := laneWorld()
	s.Routes[0].Lanes, s.Routes[1].Lanes = 4, 4
	s.Fleet.Put(laneTake("flash-a", "provider: class=rate-limited status=429 msg=too many requests", t0))
	s.Fleet.Put(laneTake("flash-b", "provider: class=other status=429 msg=slow down", t0))

	assert.Equal(t, 2, s.routeLaneLimit(s.Routes[0]), "a 429 halves the lane cap")
	assert.Equal(t, 2, s.routeLaneLimit(s.Routes[1]), "an HTTP 429 halves the cap whatever its class word")

	// the halved cap is the one the deal enforces: two lanes on flash-a fill it
	s.Fleet.Put(&Card{ID: "w1", Row: "m1", Col: Working, Fields: map[string]string{FieldRoute: "flash-a"}})
	s.Fleet.Put(&Card{ID: "w2", Row: "m1", Col: Working, Fields: map[string]string{FieldRoute: "flash-a"}})
	s = s.withLanePlan()
	set, _, why, _ := s.routeOf(laneCard("s1-1"), nil, nil)
	require.Empty(t, why)
	assert.Equal(t, "flash-b", set[FieldRoute], "two lanes fill the halved cap of two")

	// the window ends: the cap is its own again
	s.Now = t0.Add(RouteLaneRateWindow)
	assert.Equal(t, 4, s.routeLaneLimit(s.Routes[0]), "the cap restores when the window ends")

	// a credit line is no rate limit
	s2 := laneWorld()
	s2.Routes[0].Lanes = 4
	s2.Fleet.Put(laneTake("flash-a", "provider: class=out-of-credit status=402 msg=insufficient credit", t0))
	assert.Equal(t, 4, s2.routeLaneLimit(s2.Routes[0]), "a credit refusal is no rate limit")
}
