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
	_, err = h.st.FriendBeat(h.ctx, "amy", false)
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

func TestFriendBeatPersistsSleepAndOrdinaryBeatClearsIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 3}})
	require.NoError(t, err)
	b, err := h.st.FriendBeat(h.ctx, "amy", true)
	require.NoError(t, err)
	assert.True(t, b.Asleep)
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, sprint.Down, rows[0].Status, "a sleeping session is down")
	assert.Equal(t, 3, rows[0].Width)
	b, err = h.st.FriendBeat(h.ctx, "amy", false)
	require.NoError(t, err)
	assert.False(t, b.Asleep)
	rows, err = h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, sprint.Up, rows[0].Status)
}

func TestFriendRowsOrderUpHeldDownByNameAndSleepersAreDown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1}, {Name: "bob", Width: 1}, {Name: "cat", Width: 1}, {Name: "zed", Width: 1}, {Name: "eve", Width: 1}})
	require.NoError(t, err)
	_, err = h.st.FriendBeat(h.ctx, "zed", false)
	require.NoError(t, err)
	for _, name := range []string{"cat", "bob"} {
		_, err = h.st.FriendBeat(h.ctx, name, true)
		require.NoError(t, err)
	}
	require.NoError(t, h.st.SetFriendHeld(h.ctx, "eve", true, "c", "", time.Time{}, 0))
	rows, err := h.st.FriendRows(h.ctx, h.now)
	require.NoError(t, err)
	var got [][2]string
	for _, r := range rows {
		got = append(got, [2]string{r.Name, r.Status})
	}
	assert.Equal(t, [][2]string{{"zed", sprint.Up}, {"eve", sprint.Held}, {"amy", sprint.Down}, {"bob", sprint.Down}, {"cat", sprint.Down}}, got)
}
