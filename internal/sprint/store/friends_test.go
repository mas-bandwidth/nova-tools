package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FriendRows is the roster with width and status only: a friend's ready,
// working, ok and failed are her sprint cards' counts, filled by where from
// her fleet row (friend.<name>), never read or counted here — the store holds
// no job record.
func TestFriendRowsReturnsNameWidthStatusOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 3}, {Name: "bob", Width: 1}})
	require.NoError(t, err)
	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)
	require.NoError(t, h.st.SetFriendHeld(h.ctx, "bob", true, "c"))
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	// up first, then held (FleetOrder); the counts are all zero, never read
	assert.Equal(t, []FriendRow{
		{Name: "amy", Width: 3, Status: sprint.Up},
		{Name: "bob", Width: 1, Status: sprint.Held},
	}, rows)
}
