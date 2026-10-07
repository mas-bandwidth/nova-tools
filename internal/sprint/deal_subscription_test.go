package sprint_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// docs/SPEC-SPRINT.md section 1, deal-subscription-first-r-t-bb: the roster's billing
// reaches the deal, a heavy or pro card goes to an up subscription friend with room
// before an API friend takes it, and a card naming a WHO keeps that friend.
func TestHeavyAndProCardsGoToSubscriptionFriendsFirst(t *testing.T) {
	t.Parallel()
	for _, tier := range []string{"heavy", "pro"} {
		t.Run(tier, func(t *testing.T) {
			t.Parallel()
			seats := subscriptionSeats(t)
			s, cards := subscriptionWorld(tier, 5)
			plan := sprint.FriendDeal(s, cards, seats)
			require.Empty(t, plan.Refused)
			rows := dealtFriendRows(plan)
			assert.Equal(t, 4, rows[sprint.FriendRow("sub")], "the subscription friend's room is used first")
			assert.Equal(t, 1, rows[sprint.FriendRow("api")], "the API friend takes the overflow while the route remains available")

			brief := "c: named work\nREPO: mas-bandwidth/nova-tools\ntier: " + tier + "\nWHO: friend api\n\nThe task."
			pinned := &sprint.Card{ID: "named-" + tier, Row: "named-" + tier, Col: sprint.Ready, Fields: map[string]string{
				"brief": brief, sprint.FieldWho: sprint.WhoOfBrief(brief), sprint.FieldTier: tier,
			}}
			s.Work.SetRows(append(s.Work.Rows(), pinned.Row))
			s.Work.Put(pinned)
			pinnedPlan := sprint.FriendDeal(s, []*sprint.Card{pinned}, seats)
			assert.Equal(t, 1, dealtFriendRows(pinnedPlan)[sprint.FriendRow("api")], "WHO keeps its named friend")
		})
	}
}

// A flash card keeps the existing friend order: the subscription preference is only the
// heavy and pro tiers' (docs/SPEC-SPRINT.md section 1, deal-subscription-first-r-t-bb).
func TestFlashCardsKeepTheExistingFriendOrder(t *testing.T) {
	t.Parallel()
	seats := subscriptionSeats(t)
	s, cards := subscriptionWorld("flash", 2)
	rows := dealtFriendRows(sprint.FriendDeal(s, cards, seats))
	assert.Equal(t, 1, rows[sprint.FriendRow("sub")])
	assert.Equal(t, 1, rows[sprint.FriendRow("api")])
}

// subscriptionSeats is two up friends of every tier from the twin store's roster: sub at
// subscription billing and width 2 (room, DealAhead times her width), api at API billing
// and the same width, so only the billing rule can separate them.
func subscriptionSeats(t *testing.T) []sprint.FriendSeat {
	t.Helper()
	ctx := context.Background()
	st := &store.Store{B: store.NewMem(), Names: sprint.Names{Prefix: "subscription-test-"}, Actor: "test",
		Now: func() time.Time { return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) }}
	require.NoError(t, st.Init(ctx))
	_, _, _, err := st.SyncFriends(ctx, []store.FriendSpec{
		{Name: "sub", Width: 2, Class: "flash,heavy,pro", Billing: "subscription"},
		{Name: "api", Width: 2, Class: "flash,heavy,pro", Billing: "api"},
	})
	require.NoError(t, err)
	seats, err := st.FriendSeats(ctx, time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))
	require.NoError(t, err)
	require.Len(t, seats, 2)
	for i := range seats {
		seats[i].Status = sprint.Up
	}
	return seats
}

// subscriptionWorld is count ready cards of the tier with one route to run them, and the
// snapshot they are dealt in.
func subscriptionWorld(tier string, count int) (*sprint.Snapshot, []*sprint.Card) {
	work, fleet := sprint.NewTable(sprint.Work), sprint.NewTable(sprint.Fleet)
	work.SetRows([]string{tier})
	fleet.SetRows([]string{"m1"})
	s := &sprint.Snapshot{Now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC), Work: work, Fleet: fleet,
		Routes: []sprint.Route{{Name: tier + "-route", Tier: tier, Provider: "provider", Model: "model", Enabled: true}}}
	cards := make([]*sprint.Card, 0, count)
	for i := 1; i <= count; i++ {
		id := fmt.Sprintf("%s-%d", tier, i)
		brief := "c: " + id + "\nREPO: mas-bandwidth/nova-tools\ntier: " + tier + "\n\nThe task."
		card := &sprint.Card{ID: id, Row: tier, Col: sprint.Ready, Fields: map[string]string{"brief": brief, sprint.FieldTier: tier}}
		work.Put(card)
		cards = append(cards, card)
	}
	return s, cards
}

// dealtFriendRows is how many cards the plan creates on each friend's fleet row.
func dealtFriendRows(plan sprint.Plan) map[string]int {
	rows := map[string]int{}
	for _, unit := range plan.Units {
		for _, change := range unit.Changes {
			if change.Table == sprint.Fleet && change.Entry.Create != nil {
				rows[change.Entry.Create.Row]++
			}
		}
	}
	return rows
}
