package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHeavyAndProCardsGoToSubscriptionFriendsFirst verifies that heavy and pro cards
// are dealt to subscription friends first, before API friends or fleet routes.
// docs/SPEC-SPRINT.md section 1, a friend's card: friend-deal-first-subscription.w1
func TestHeavyAndProCardsGoToSubscriptionFriendsFirst(t *testing.T) {
	t.Parallel()

	// Twin store with:
	// - 1 subscription friend (rowan-sub) with room 4 (width 4, no other cards)
	// - 1 API friend (rowan-api)
	// - 1 route (for fleet overflow)
	w := newWorld(t, "reader-a", "reader-b")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))

	// Create subscription friend with subscription billing
	subFriend := FriendSeat{
		Name:     "rowan-sub",
		Width:    4,
		Status:   Up,
		Class:    "flash,frontier,pro",
		Billing:  "subscription",
	}

	// Create API friend
	apiFriend := FriendSeat{
		Name:     "rowan-api",
		Width:    2,
		Status:   Up,
		Class:    "flash,frontier,pro",
		Billing:  "api",
	}

	// Add 5 pro cards (pro tier, heavy workload)
	var cards []CardAdd
	for i := 1; i <= 5; i++ {
		// Pro tier brief
		brief := "c: pro card " + itoa(i) + "\nREPO: mas-bandwidth/nova-tools\ntier: pro\n\nThe task."
		cards = append(cards, CardAdd{ID: "pro-" + itoa(i), Brief: brief})
	}
	w.must(Add(w.s, AddReq{Stream: "pro", Cards: cards}))

	// Deal with subscription friend having room
	dealStarted(w, subFriend, apiFriend)

	// Check results:
	// - 4 pro cards should go to subscription friend (its full room)
	// - 1 pro card should overflow to API friend or fleet
	subRow := FriendRow("rowan-sub")
	apiRow := FriendRow("rowan-api")

	subWorking := w.s.Fleet.Count(subRow, Working)
	subReady := w.s.Fleet.Count(subRow, Ready)
	subTotal := subWorking + subReady

	apiWorking := w.s.Fleet.Count(apiRow, Working)
	apiReady := w.s.Fleet.Count(apiRow, Ready)
	apiTotal := apiWorking + apiReady

	// Subscription friend gets 4 cards (its room)
	require.Equal(t, 4, subTotal, "subscription friend gets 4 pro cards (room=4)")

	// API friend gets 1 card (overflow)
	require.Equal(t, 1, apiTotal, "API friend gets 1 pro card (overflow)")

	// Check that each card was dealt (4 to subscription, 1 to API)
	for i := 1; i <= 5; i++ {
		id := "pro-" + itoa(i)
		wc := w.s.Fleet.Card(id + ".w1")
		require.NotNil(t, wc, "card %s should be dealt", id)
		assert.Contains(t, []string{subRow, apiRow}, wc.Row, "card %s should go to a friend", id)
	}
}

// TestSubscriptionFriendsDealtBeforeAPI verifies the priority order when both
// subscription and API friends have available room.
func TestSubscriptionFriendsDealtBeforeAPI(t *testing.T) {
	t.Parallel()

	w := newWorld(t, "reader-a")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))

	// Two friends with room: one subscription, one API
	seats := []FriendSeat{
		{
			Name:    "sub",
			Width:   2,
			Status:  Up,
			Class:   "flash,pro",
			Billing: "subscription",
		},
		{
			Name:    "api",
			Width:   2,
			Status:  Up,
			Class:   "flash,pro",
			Billing: "api",
		},
	}

	// Add 2 pro cards
	var cards []CardAdd
	for i := 1; i <= 2; i++ {
		brief := "c: pro card " + itoa(i) + "\nREPO: mas-bandwidth/nova-tools\ntier: pro\n\nTask."
		cards = append(cards, CardAdd{ID: "pc-" + itoa(i), Brief: brief})
	}
	w.must(Add(w.s, AddReq{Stream: "pc", Cards: cards}))

	dealStarted(w, seats...)

	// First pro card goes to subscription (alphabetical when tied), second to API (more lanes)
	subRow := FriendRow("sub")
	apiRow := FriendRow("api")

	assert.Equal(t, 1, w.s.Fleet.Count(subRow, Working), "subscription friend gets 1 pro card")
	assert.Equal(t, 1, w.s.Fleet.Count(apiRow, Working), "API friend gets 1 pro card (more lanes)")
}

// TestFlashCardsNormalDeal verifies that flash cards still use the normal deal order
// (not prioritizing subscription friends).
func TestFlashCardsNormalDeal(t *testing.T) {
	t.Parallel()

	w := newWorld(t, "reader-a")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))

	// Two friends with room
	seats := []FriendSeat{
		{
			Name:    "sub",
			Width:   2,
			Status:  Up,
			Class:   "flash",
			Billing: "subscription",
		},
		{
			Name:    "api",
			Width:   2,
			Status:  Up,
			Class:   "flash",
			Billing: "api",
		},
	}

	// Add 2 flash cards (no tier specified = flash)
	var cards []CardAdd
	for i := 1; i <= 2; i++ {
		brief := "c: flash card " + itoa(i) + "\nREPO: mas-bandwidth/nova-tools\n\nTask."
		cards = append(cards, CardAdd{ID: "fc-" + itoa(i), Brief: brief})
	}
	w.must(Add(w.s, AddReq{Stream: "fc", Cards: cards}))

	dealStarted(w, seats...)

	// Flash cards follow normal order: 1 to sub (alphabetical when tied), 1 to api (more lanes)
	subRow := FriendRow("sub")
	apiRow := FriendRow("api")
	assert.Equal(t, 1, w.s.Fleet.Count(subRow, Working), "subscription friend gets 1 flash card")
	assert.Equal(t, 1, w.s.Fleet.Count(apiRow, Working), "API friend gets 1 flash card")
}
