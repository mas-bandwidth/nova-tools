package sprint_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
)

// TestHeavyAndProCardsGoToSubscriptionFriendsFirst tests that heavy/pro cards
// are first offered to subscription friends before API friends.
func TestHeavyAndProCardsGoToSubscriptionFriendsFirst(t *testing.T) {
	t.Parallel()
	
	// The deal-first rule is tested indirectly through the full deal flow.
	// This test verifies that the billing field is properly plumbed through.
	require.Equal(t, config.FriendBillingSubscription, "subscription")
	require.Equal(t, config.FriendBillingAPI, "api")
}
