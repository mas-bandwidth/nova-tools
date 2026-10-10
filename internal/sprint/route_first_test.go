package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDealHonoursTheRouteFirstField is the route's first field
// (docs/SPEC-SPRINT.md, the deal): within a tier the route with first set
// deals before the others, and a card that has already drawn it takes the next.
func TestDealHonoursTheRouteFirstField(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Now:   t0,
		Fleet: NewTable(Fleet),
		Routes: []Route{
			{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "a", Enabled: true},
			{Name: "flash-b", Tier: cardhdr.RouteFlash, Provider: "q", Model: "b", Enabled: true, First: true},
		},
		Tiers: map[string][]string{cardhdr.RouteFlash: {"flash-a", "flash-b"}},
	}
	fresh := func(id string) *Card {
		return &Card{ID: id, Fields: map[string]string{"brief": "tier: flash\n"}}
	}
	for _, id := range []string{"s1-1", "s1-2"} {
		set, tier, why, _ := s.routeOf(fresh(id), nil, nil, "")
		require.Empty(t, why, id)
		assert.Equal(t, cardhdr.RouteFlash, tier, id)
		assert.Equal(t, "flash-b", set[FieldRoute], "the route with first set deals before flash-a, %s", id)
		assert.Equal(t, "q/b", set[FieldModel], id)
	}
	again := &Card{ID: "s1-1", Fields: map[string]string{"brief": "tier: flash\n", FieldRoutes: "flash-b"}}
	set, _, why, _ := s.routeOf(again, nil, nil, "")
	require.Empty(t, why)
	assert.Equal(t, "flash-a", set[FieldRoute], "a card that already drew the first route takes the other")

	s.Routes[1].First = false
	set, _, why, _ = s.routeOf(fresh("s1-3"), nil, nil, "")
	require.Empty(t, why)
	assert.Equal(t, "flash-a", set[FieldRoute], "with first unset the walk starts at the index")
}
