package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHeavyAndProCardsGoToSubscriptionFriendsFirst verifies that heavy and pro cards
// are dealt to friends with subscription billing first, before api-billed friends.
// Empty billing is treated as subscription per docs/SPEC-SPRINT.md.
func TestHeavyAndProCardsGoToSubscriptionFriendsFirst(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, "c: work tier: pro\n\nThe task.")

	// Setup: one subscription friend with room 4, one api friend with room 4
	subFriend := FriendSeat{
		Name:     "sub",
		Width:    4,
		Status:   Up,
		Tiers:    []string{cardhdr.RoutePro},
		Billing:  "subscription", // explicit subscription
	}

	apiFriend := FriendSeat{
		Name:     "api",
		Width:    4,
		Status:   Up,
		Tiers:    []string{cardhdr.RoutePro},
		Billing:  "api", // explicit api billing
	}

	// Also test with empty billing (should default to subscription)
	emptyBillingFriend := FriendSeat{
		Name:     "empty",
		Width:    4,
		Status:   Up,
		Tiers:    []string{cardhdr.RoutePro},
		Billing:  "", // empty - should default to subscription
	}

	// Add 5 pro cards
	var cards []*Card
	for i := 1; i <= 5; i++ {
		id := "s1-" + itoa(i)
		w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{{ID: id, Brief: "c: work tier: pro\n\nThe task."}}}))
		cards = append(cards, w.s.Primary(id))
	}

	// Deal to subscription friend first, then empty billing (treated as subscription), then api
	p, _, _ := friendDeal(w.s, cards, []FriendSeat{subFriend, apiFriend, emptyBillingFriend})
	w.must(p)

	// 4 cards should go to subscription friends (sub + empty), 1 to api
	subDealt := 0
	emptyDealt := 0
	apiDealt := 0

	for _, row := range w.s.Fleet.Rows() {
		if row.Row == FriendRow("sub") && row.Col == Working {
			subDealt++
		}
		if row.Row == FriendRow("empty") && row.Col == Working {
			emptyDealt++
		}
		if row.Row == FriendRow("api") && row.Col == Working {
			apiDealt++
		}
	}

	// Subscription friends should get 4 cards (with room 4 each)
	// API friend should get the remaining 1 card
	assert.Equal(t, 4, subDealt+emptyDealt, "subscription friends should get 4 cards first")
	assert.Equal(t, 1, apiDealt, "api friend should get 1 card")
}
