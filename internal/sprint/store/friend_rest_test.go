package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's card is never withdrawn for its route's rest: a friend runs her own model, so
// no fleet route's rest is hers. On 2026-10-04 cards dealt to the fleet (a route on each)
// were withdrawn when their provider ran out of credit, and the friends' deal placed the
// same work cards on a friend's row with the fleet route still on them, ready behind her
// width; every tick then withdrew each one for that route's rest and the friends' deal placed
// it again (generation 2456 by 10:52 PM), each placing a new copy in the friend's inbox that
// her runner started beside the last.
func TestAFriendsCardIsNeverWithdrawnForARestingRoute(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, providerRoute("or-a", "flash", "openrouter"))
	ids := []string{"a", "b", "c"}
	for _, id := range ids {
		h.must(AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{{ID: id, Brief: id + " tier: flash\nWHO: friend amy\n\nWork."}}}))
	}
	h.startMachine()
	h.machine() // no friend yet: the fleet, on or-a
	for _, id := range ids {
		require.Equal(t, "or-a", h.snap().Fleet.Card(id+".w1").F(sprint.FieldRoute), "%s: dealt to the fleet on its route", id)
	}
	h.must(RouteRestStep(sprint.RouteRestReq{Target: "openrouter", Reason: "out of funds", Who: "boss"})) // the provider rests
	h.machine()                                                                                           // the fleet's ready cards are withdrawn
	for _, id := range ids {
		require.Equal(t, sprint.Withdrawn, h.snap().Fleet.Card(id+".w1").Col, "%s: withdrawn for the rest", id)
	}

	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1, Class: "flash"}})
	require.NoError(t, err)
	beatTick := func() {
		h.up("amy")
		h.machine()
	}
	h.startMachine() // every provider out of credit and no friend up stopped it
	beatTick()       // the friends' deal places them on her row
	gens, cols := map[string]int{}, map[string]string{}
	for _, id := range ids {
		wc := h.snap().Fleet.Card(id + ".w1")
		if wc.Row == "friend.amy" && (wc.Col == sprint.Ready || wc.Col == sprint.Working) {
			gens[wc.ID], cols[wc.ID] = wc.Int("gen"), wc.Col
		}
	}
	require.NotEmpty(t, gens, "dealt to her")
	for range 3 {
		beatTick()
	}
	for id, gen := range gens {
		wc := h.snap().Fleet.Card(id)
		assert.Equal(t, "friend.amy", wc.Row, "%s: still hers", id)
		assert.Equal(t, cols[id], wc.Col, "%s: where it was", id)
		assert.Equal(t, gen, wc.Int("gen"), "%s: never taken back and dealt again", id)
		assert.Empty(t, wc.F(sprint.FieldRoute), "%s: no fleet route on a friend's card", id)
	}
	h.clean("a friend's card on a resting route")
}
