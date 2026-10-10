package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Friends first (docs/SPEC-SPRINT.md, WHO preference): a ready card is offered to the
// friends before the machines' deal, so when every machine route of its tier rests (its
// provider out of funds) an up friend whose tiers hold the tier and who has free width is
// dealt it, and the tick raises no "no route serves the tier" hold for it. Without the
// friend the same tick holds it under that judgment: the control that the routes bite.
func TestAReadyCardGoesToAFriendWhenNoMachineRouteServesItsTier(t *testing.T) {
	t.Parallel()
	setup := func() *world {
		w := friendWorld(t, "c: work tier: pro\n\nThe task.")
		w.s.Routes = []Route{{Name: "pro-a", Tier: cardhdr.RoutePro, Provider: "p", Model: "m", Enabled: true}}
		out := RouteRest{At: w.s.Now, Until: OpenUntil, Cards: []string{"c1"}, Cause: RestCredit, Why: "out of credit: provider p refused card c1"}
		w.s.Fleet.SetProps(map[string]string{PropProviderRest("p"): out.value()})
		ws, _ := w.s.withRests()
		_, why := ws.noRoute(w.s.Primary("s1-1"))
		require.NotEmpty(t, why, "every machine route of tier pro rests")
		return w
	}
	heldForTier := func(w *world) bool {
		for _, n := range w.notesOf(NNoRoute) {
			if n.Stream == TierSubject(cardhdr.RoutePro) && contains(n.Primaries, "s1-1") {
				return true
			}
		}
		return false
	}

	control := setup()
	dealStarted(control)
	require.Equal(t, Ready, control.s.StateOf("s1-1"), "no friend: it waits ready")
	require.True(t, heldForTier(control), "no friend: held by the tier's no-route judgment")

	w := setup()
	dealStarted(w,
		FriendSeat{Name: "bob", Width: 4, Status: Up, Class: "flash", Tiers: []string{cardhdr.RouteFlash}},
		FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "pro", Tiers: []string{cardhdr.RouteFlash, cardhdr.RoutePro}})
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc, "dealt")
	assert.Equal(t, FriendRow("amy"), wc.Row, "the friend whose tiers hold pro, not the one with more room")
	assert.Equal(t, Working, wc.Col, "a lane free: working once she starts it")
	assert.Equal(t, Working, w.s.StateOf("s1-1"))
	assert.False(t, heldForTier(w), "no no-route hold for a card a friend took")
}
