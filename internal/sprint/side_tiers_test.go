package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// sideTierWorld is a world with a flash and a pro route enabled and the fleet's tiers set
// to flash (set --fleet-tiers flash).
func sideTierWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.s.Routes = []Route{
		{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "f", Enabled: true},
		{Name: "pro-a", Tier: cardhdr.RoutePro, Provider: "p", Model: "pr", Enabled: true},
	}
	w.s.Work.SetProp(PropFleetTiers, cardhdr.RouteFlash)
	return w
}

func sidePrimary(brief string) *Card {
	return &Card{ID: "s1-1", Row: "s1", Fields: map[string]string{"kind": "primary", "attempt": "0", "stream": "s1", "brief": brief}}
}

// A card pinned to a model is no exception to the fleet's tiers: the set is the owner's
// switch. A pro card pinned to a model is not drawn for a machine under --fleet-tiers
// flash; a flash card pinned to one still runs on its pin.
func TestAModelPinDoesNotOverrideTheFleetsTiers(t *testing.T) {
	t.Parallel()
	w := sideTierWorld(t)
	set, tier, why, _ := w.s.routeOf(sidePrimary("c: a card tier: pro\nmodel: p/m\ntokens: 1000\ndeadline: 600\n"), nil, nil)
	assert.Nil(t, set, "no pin route for a tier the fleet's set leaves out")
	assert.Equal(t, cardhdr.RoutePro, tier)
	assert.Contains(t, why, "--fleet-tiers")

	set, _, why, _ = w.s.routeOf(sidePrimary("c: a card\nmodel: p/m\ntokens: 1000\ndeadline: 600\n"), nil, nil)
	require.Empty(t, why)
	assert.Equal(t, RoutePin, set[FieldRoute], "a flash card pinned to a model runs on its pin")
}

// A frontier card's judgment is the same whatever the fleet's tiers: under --fleet-tiers
// flash and under all, a frontier friend up makes it the friends' deal's, and no machine
// draws a route for it (2026-10-10: the refusal came before the friend check and stranded
// every frontier card while stella was up with frontier).
func TestAFrontierCardsJudgmentIsUnchangedByTheFleetsTiers(t *testing.T) {
	t.Parallel()
	w := sideTierWorld(t)
	w.s.Friends = []FriendSeat{{Name: "fay", Width: 2, Status: Up, Tiers: []string{cardhdr.RouteFrontier}}}
	c := sidePrimary("c: a card tier: frontier\n")
	set, tier, why, byFriend := w.s.routeOf(c, nil, nil)
	w.s.Work.SetProp(PropFleetTiers, TiersAll)
	setAll, tierAll, whyAll, byFriendAll := w.s.routeOf(c, nil, nil)
	assert.Nil(t, set)
	assert.Nil(t, setAll)
	assert.Equal(t, cardhdr.RouteFrontier, tier)
	assert.Contains(t, why, "a frontier card is never a machine's; a friend up serves frontier (fay)")
	assert.True(t, byFriend)
	assert.Equal(t, []any{tierAll, whyAll, byFriendAll}, []any{tier, why, byFriend}, "unchanged by --fleet-tiers flash")
}
