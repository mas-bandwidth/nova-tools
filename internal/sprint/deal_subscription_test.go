package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/config"
)

// TestHeavyAndProCardsGoToSubscriptionFriendsFirst pins the subscription-first deal
// (docs/SPEC-SPRINT.md section 1, deal-subscription-first-r-t-bb): a heavy or pro card is
// offered to an up subscription friend with room before an api friend or a fleet route
// takes it. On the twin store, one subscription friend with room 4 (width 2, batch), one
// api friend with more room and the fleet's routes: five pro cards go four to the
// subscription friend and one elsewhere. The api friend takes none while the subscription
// friend has room, and it is an explicit api, never the default. A friend whose row names
// no billing is the documented default, subscription (config.DefaultFriendBilling), and is
// offered the cards first too.
func TestHeavyAndProCardsGoToSubscriptionFriendsFirst(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		billing string
	}{
		{"explicit subscription", config.FriendBillingSubscription},
		{"default subscription", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			briefs := make([]string, 5)
			for i := range briefs {
				briefs[i] = fleetBrief(cardhdr.RoutePro)
			}
			w := friendWorld(t, briefs...)
			// room 4: width 2 in batch mode, DealAhead times her width
			sub := FriendSeat{Name: "sub", Width: 2, Status: Up, Class: cardhdr.RoutePro, Tiers: []string{cardhdr.RoutePro}, Billing: tc.billing}
			// more room than the subscription friend: without the rule she would take every card
			api := FriendSeat{Name: "api", Width: 4, Status: Up, Class: cardhdr.RoutePro, Tiers: []string{cardhdr.RoutePro}, Billing: config.FriendBillingAPI}
			dealWith(w, sub, api)

			subCards := workOn(w, FriendRow("sub"))
			assert.Equal(t, 4, subCards, "the subscription friend takes her room of pro cards before the api friend")
			apiCards := workOn(w, FriendRow("api"))
			fleetCards := workOn(w, "m1") + workOn(w, "m2")
			assert.Equal(t, 1, apiCards+fleetCards, "the fifth pro card is what is left for the api friend or a fleet route")
			assert.Equal(t, 5, subCards+apiCards+fleetCards, "every pro card is dealt")
		})
	}
}
