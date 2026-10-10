package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The friends' reclaim leaves a card whose route rests (docs/SPEC-SPRINT.md section 1, a
// friend's card): the tick's own rest withdrawal (restWithdrawals) takes it back in the same
// plan, and a card moved to two places refuses the whole tick. One flash route on provider p
// rests, m1 holds four cards dealt ahead on it, and a flash friend up at width 2: the tick
// succeeds, and she reclaims nothing from the resting route.
func TestTheReclaimLeavesACardWhoseRouteRests(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.mu.Lock()
	h.live = []string{"m1"}
	h.mu.Unlock()
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 2}))
	h.m.SetRoutes([]sprint.Route{providerRoute("p-flash", "flash", "p")})
	h.addReady("s1", 4, briefOf("flash", ""))
	h.must(DealStep(sprint.DealReq{}))
	ahead := h.snap().Fleet.Cell("m1", sprint.Ready)
	require.Len(t, ahead, 4, "m1 is dealt ahead four")
	for _, c := range ahead {
		require.Equal(t, "p-flash", c.F(sprint.FieldRoute))
	}

	// provider p rests for half an hour (its funds low): the coordinator's routes rest
	at := h.st.Now()
	rest := at.UTC().Format(time.RFC3339) + " " + at.Add(30*time.Minute).UTC().Format(time.RFC3339) + " - " + sprint.RestCoordinator + " rested by boss: low on funds"
	st := DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"none"}}})
	st.Plan = func(s *sprint.Snapshot) sprint.Plan {
		was, ok := s.Fleet.Prop(sprint.PropProviderRest("p"))
		return sprint.Plan{Props: []sprint.PropWrite{{Table: sprint.Fleet, Name: sprint.PropProviderRest("p"), Value: rest, Was: was, WasAbsent: !ok}}}
	}
	h.must(st)

	amy := []sprint.FriendSeat{{Name: "amy", Width: 2, Status: sprint.Up, Class: "flash", Tiers: []string{"flash"}}}
	var due int
	deal := TickPartStep("deal", sprint.TickDeal, sprint.TickReq{Friends: amy}, nil, nil, &due)
	deal.Routes = true // the tick's deal reads the routes, as the server's view gives them (routesPart)
	h.must(deal)
	s := h.snap()
	assert.Zero(t, s.Fleet.Count(sprint.FriendRow("amy"), sprint.Ready)+s.Fleet.Count(sprint.FriendRow("amy"), sprint.Working), "nothing reclaimed from the resting route")
	for _, c := range ahead {
		assert.Equal(t, sprint.Withdrawn, s.Fleet.Card(c.ID).Col, "%s is withdrawn by the rest, in the same tick", c.ID)
	}
	h.clean("a tick with a resting route and a friend's idle lanes")
}
