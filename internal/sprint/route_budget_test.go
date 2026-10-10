package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRouteLaneRoom pins the pure admission of a metered route (route_budget.go): rpm 0 is
// unmetered and admits at any count, and a metered route admits a new lane only while its
// lanes in flight are under max(1, rpm/LaneRPM): a 10 rpm route runs one lane, a 40 rpm route
// five.
func TestRouteLaneRoom(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		rpm, inFlight int
		want          bool
	}{
		{"rpm 0 admits at any count", 0, 0, true},
		{"rpm 0 admits at a large count", 0, 40, true},
		{"rpm 10 admits at 0", 10, 0, true},
		{"rpm 10 refuses at 1", 10, 1, false},
		{"rpm 40 admits at 4", 40, 4, true},
		{"rpm 40 refuses at 5", 40, 5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, RouteLaneRoom(tc.rpm, tc.inFlight), "rpm=%d inFlight=%d", tc.rpm, tc.inFlight)
		})
	}
}

// TestAMeteredRouteRunsOneLaneAtTenRPM: a 10 rpm route admits one lane, so of two flash cards
// the first is dealt on it and the second waits with the budget reason.
func TestAMeteredRouteRunsOneLaneAtTenRPM(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Now:   t0,
		Fleet: NewTable(Fleet),
		Routes: []Route{
			{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "a", Enabled: true, RPM: 10},
		},
		Tiers: map[string][]string{cardhdr.RouteFlash: {"flash-a"}},
	}
	s.Fleet.SetRows([]string{"m1"})
	fresh := func(id string) *Card {
		return &Card{ID: id, Fields: map[string]string{"brief": "tier: flash\n"}}
	}
	set, tier, why, _ := s.routeOf(fresh("s1-1"), nil, nil)
	require.Empty(t, why)
	assert.Equal(t, cardhdr.RouteFlash, tier)
	assert.Equal(t, "flash-a", set[FieldRoute], "the first lane is dealt on the metered route")
	// the first lane is in flight now: the second card has no room
	s.Fleet.Put(&Card{ID: "s1-1.w1", Row: "m1", Col: Ready, Fields: map[string]string{FieldRoute: "flash-a"}})
	_, _, why, _ = s.routeOf(fresh("s1-2"), nil, nil)
	assert.Equal(t, "every route of "+cardhdr.RouteFlash+" is at its requests-per-minute budget", why)
}

// TestAMeteredRouteFallsThroughToTheNext: a metered route at its budget is skipped for the
// deal, and an unmetered route of the same tier takes the card.
func TestAMeteredRouteFallsThroughToTheNext(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Now:   t0,
		Fleet: NewTable(Fleet),
		Routes: []Route{
			{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "a", Enabled: true, RPM: 10},
			{Name: "flash-b", Tier: cardhdr.RouteFlash, Provider: "q", Model: "b", Enabled: true},
		},
		Tiers: map[string][]string{cardhdr.RouteFlash: {"flash-a", "flash-b"}},
	}
	s.Fleet.SetRows([]string{"m1"})
	s.Fleet.Put(&Card{ID: "x.w1", Row: "m1", Col: Working, Fields: map[string]string{FieldRoute: "flash-a"}})
	set, _, why, _ := s.routeOf(&Card{ID: "s1-1", Fields: map[string]string{"brief": "tier: flash\n"}}, nil, nil)
	require.Empty(t, why)
	assert.Equal(t, "flash-b", set[FieldRoute], "the metered route full, the unmetered route of the tier takes the card")
}

// TestAnUnmeteredRouteIsUnchanged: rpm 0 deals exactly as before, whatever lanes are in flight.
func TestAnUnmeteredRouteIsUnchanged(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Now:   t0,
		Fleet: NewTable(Fleet),
		Routes: []Route{
			{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "a", Enabled: true},
		},
		Tiers: map[string][]string{cardhdr.RouteFlash: {"flash-a"}},
	}
	s.Fleet.SetRows([]string{"m1"})
	for i := range 5 {
		s.Fleet.Put(&Card{ID: "lane-" + itoa(i) + ".w1", Row: "m1", Col: Ready, Fields: map[string]string{FieldRoute: "flash-a"}})
	}
	set, tier, why, _ := s.routeOf(&Card{ID: "s1-1", Fields: map[string]string{"brief": "tier: flash\n"}}, nil, nil)
	require.Empty(t, why)
	assert.Equal(t, cardhdr.RouteFlash, tier)
	assert.Equal(t, "flash-a", set[FieldRoute], "an unmetered route deals as before")
}
