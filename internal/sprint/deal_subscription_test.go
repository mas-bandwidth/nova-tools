package sprint_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// TestHeavyAndProCardsGoToSubscriptionFriendsFirst pins the subscription-first deal
// (docs/SPEC-SPRINT.md section 1, deal-subscription-first-r-t-bb): a heavy or pro card is
// offered to an up subscription friend with room before an api friend or a fleet route
// takes it. The test drives the twin store's roster path: nova-config's friend rows (friend
// sync) are stored on the friends table and read by Store.FriendSeats, which resolves an
// omitted billing word to the documented default, subscription, so the deal offers such a
// friend the cards first. One subscription friend with room 4 (width 2, batch: DealAhead
// times her width), one api friend with more room (width 4) and the fleet's routes: five pro
// cards go four to the subscription friend and the fifth to the api friend or a route. A
// friend whose stored row names no billing is the default, subscription, and is offered the
// cards first too; only an explicit api is api.
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
			r := newHoldRig(t, 0, 0)
			// the roster as nova-config's friend rows leave it (friend sync): amy is the
			// subscription friend whose billing is the word the case gives (empty for the
			// documented default), bob an explicit api friend with more room than amy.
			_, _, _, err := r.st.SyncFriends(r.ctx, []store.FriendSpec{
				{Name: "amy", Width: 2, Class: cardhdr.RoutePro, Billing: tc.billing},
				{Name: "bob", Width: 4, Class: cardhdr.RoutePro, Billing: config.FriendBillingAPI},
			})
			require.NoError(t, err)

			// the roster boundary: the seat the deal is handed carries an omitted billing
			// resolved to the documented default, subscription, never left empty.
			seats, err := r.st.FriendSeats(r.ctx, r.st.Now())
			require.NoError(t, err)
			got := map[string]sprint.FriendSeat{}
			for _, s := range seats {
				got[s.Name] = s
			}
			want := config.FriendBillingSubscription
			if tc.billing != "" {
				want = tc.billing
			}
			require.Equal(t, want, got["amy"].Billing, "an omitted billing is the documented default, subscription, where the roster is read")
			require.Equal(t, config.FriendBillingAPI, got["bob"].Billing, "an explicit api stays api")

			var cards []sprint.CardAdd
			for i := range 5 {
				cards = append(cards, sprint.CardAdd{ID: fmt.Sprintf("pro-%d", i+1),
					Brief: "c: a fleet card tier: " + cardhdr.RoutePro + "\nREPO: mas-bandwidth/nova-tools\n\nThe task."})
			}
			r.must(store.AddStep(sprint.AddReq{Stream: "pros", Cards: cards}))
			r.tick()

			s := r.snap()
			subCards := workCardsOn(s, sprint.FriendRow("amy"))
			apiCards := workCardsOn(s, sprint.FriendRow("bob"))
			fleetCards := workCardsOn(s, "m1") + workCardsOn(s, "m2")
			assert.Equal(t, 4, subCards, "the subscription friend takes her room of pro cards before the api friend")
			assert.Equal(t, 1, apiCards+fleetCards, "the fifth pro card is what is left for the api friend or a fleet route")
			assert.Equal(t, 5, subCards+apiCards+fleetCards, "every pro card is dealt")
		})
	}
}

// workCardsOn is the work cards on a fleet row, ready and working.
func workCardsOn(s *sprint.Snapshot, row string) int {
	n := 0
	for _, col := range []string{sprint.Ready, sprint.Working} {
		for _, c := range s.Fleet.Cell(row, col) {
			if c.F("kind") == "work" {
				n++
			}
		}
	}
	return n
}
