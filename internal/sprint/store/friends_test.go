package store

import (
	"testing"
	"time"

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
	require.NoError(t, h.st.SetFriendHeld(h.ctx, "bob", true, "c", "", time.Time{}, 0))
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	// up first, then held (FleetOrder); the counts are all zero, never read; her beat's time
	// is carried (view coordinator reads how stale her report is)
	assert.Equal(t, []FriendRow{
		{Name: "amy", Width: 3, Status: sprint.Up, Beat: h.now.UTC().Truncate(time.Second)},
		{Name: "bob", Width: 1, Status: sprint.Held},
	}, rows)
}

// A friend's restriction rides friend sync into her roster entry and is read back by
// FriendSpecOf and FriendRows; a change of it alone is an update, and none is none.
func TestFriendSyncCarriesTheRestriction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	spec := FriendSpec{Name: "amy", Width: 2, Streams: []string{"security*"}, Kinds: []string{"audit"}}
	added, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{spec})
	require.NoError(t, err)
	assert.Equal(t, []string{"amy"}, added)
	got, err := h.st.FriendSpecOf(h.ctx, "amy")
	require.NoError(t, err)
	assert.Equal(t, spec, got)
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, []string{"security*"}, rows[0].Streams)
	assert.Equal(t, []string{"audit"}, rows[0].Kinds)

	_, _, updated, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2}})
	require.NoError(t, err)
	assert.Equal(t, []string{"amy"}, updated, "lifting the restriction is an update")
	got, err = h.st.FriendSpecOf(h.ctx, "amy")
	require.NoError(t, err)
	assert.Empty(t, got.Streams)
	assert.Empty(t, got.Kinds)
}
