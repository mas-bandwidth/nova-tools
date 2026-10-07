package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// FriendTokensSumPerFriendFromTheResultUsage tests that the FriendRow struct
// includes a TokensTotal field for tracking token sums from card usage.
// The field will be populated from landed cards' usage records (input+output+
// cache_read+cache_write). Subscription friends show token counts; api-billed
// friends (TODO: switch on billing field) show dollars to the cent, rounded up.
func TestFriendTokensSumPerFriendFromTheResultUsage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Sync friends
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{
		{Name: "amy", Width: 1},
		{Name: "bob", Width: 1},
	})
	require.NoError(t, err)
	h.up("amy")
	h.up("bob")

	// Read rows and verify TokensTotal field exists and is zero for friends with no cards
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rows, 2)

	for _, row := range rows {
		require.NotNil(t, row.Name)
		require.Equal(t, 0, row.TokensTotal, "friend with no cards should have zero token sum")
	}

	// Test token parsing helper
	input, output, total, ok := usageTokens("input=100 output=50 cache_read=20 cache_write=10")
	require.True(t, ok)
	require.Equal(t, int64(100), input)
	require.Equal(t, int64(50), output)
	require.Equal(t, int64(180), total)
}
