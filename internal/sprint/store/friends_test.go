package store

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A failed friend removal writes nothing: the roster, the job records and the
// removed friend's beat and jobs records land in one exchange, so a lost write
// leaves the friend in the roster and the next sync finds its records to
// remove, and teardown then leaves no deployment key.
func TestFriendSyncAFailedRemovalIsDiscoveredAndTornDown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := h.ctx

	_, _, _, err := h.st.SyncFriends(ctx, []FriendSpec{{Name: "friend-a", Width: 1, Jobs: []FriendJob{{ID: "j1", State: JobReady}}}})
	require.NoError(t, err)
	_, err = h.st.FriendBeat(ctx, "friend-a")
	require.NoError(t, err)
	require.True(t, slices.Contains(h.m.Keys(h.st.Names), h.st.Names.Key(friendBeatKey("friend-a"))))
	require.True(t, slices.Contains(h.m.Keys(h.st.Names), h.st.Names.Key(friendJobsKey("friend-a"))))

	// The first removal write is lost: the roster, the job records and the
	// beat stay put, so the friend is still discoverable by the next sync.
	failed := false
	h.m.Fail = func(point string) error {
		if point == "setkeys" && !failed {
			failed = true
			return errors.New("the removal write was lost")
		}
		return nil
	}
	_, _, _, err = h.st.SyncFriends(ctx, nil)
	require.Error(t, err)
	r, _, err := h.st.roster(ctx)
	require.NoError(t, err)
	_, present := r["friend-a"]
	require.True(t, present, "a failed removal must leave the friend in the roster")
	_, ok, err := h.m.GetKey(ctx, friendBeatKey("friend-a"))
	require.NoError(t, err)
	require.True(t, ok, "a failed removal must leave the friend's beat")
	_, ok, err = h.m.GetKey(ctx, friendJobsKey("friend-a"))
	require.NoError(t, err)
	require.True(t, ok, "a failed removal must leave the friend's jobs")

	// The retry finds the friend to remove and takes its records off.
	h.m.Fail = nil
	_, removed, _, err := h.st.SyncFriends(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"friend-a"}, removed)
	_, ok, err = h.m.GetKey(ctx, friendBeatKey("friend-a"))
	require.NoError(t, err)
	require.False(t, ok, "the removed friend's beat is gone")
	_, ok, err = h.m.GetKey(ctx, friendJobsKey("friend-a"))
	require.NoError(t, err)
	require.False(t, ok, "the removed friend's jobs are gone")

	// Teardown leaves no key of the friend, orphaned or not.
	_, err = h.st.Teardown(ctx)
	require.NoError(t, err)
	for _, k := range h.m.Keys(h.st.Names) {
		require.False(t, strings.Contains(k, "friend"), "teardown left a friend key: %s", k)
	}
}

// One changed friend and many changed friends each make one write exchange:
// the roster, every job record and every removed friend's records are one
// batched write, never one per changed friend.
func TestFriendSyncBatchesJobWritesIntoOneExchange(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := h.ctx

	before := h.m.Calls["setkeys"]
	_, _, _, err := h.st.SyncFriends(ctx, []FriendSpec{{Name: "a", Width: 1, Jobs: []FriendJob{{ID: "j1", State: JobReady}}}})
	require.NoError(t, err)
	require.Equal(t, 1, h.m.Calls["setkeys"]-before, "one changed friend is one write exchange")

	before = h.m.Calls["setkeys"]
	_, _, _, err = h.st.SyncFriends(ctx, []FriendSpec{
		{Name: "a", Width: 1, Jobs: []FriendJob{{ID: "j1", State: JobReady}, {ID: "j2", State: JobWorking}}},
		{Name: "b", Width: 1, Jobs: []FriendJob{{ID: "j1", State: JobDone, OK: true}}},
		{Name: "c", Width: 2, Jobs: []FriendJob{{ID: "j1", State: JobReady}}},
	})
	require.NoError(t, err)
	require.Equal(t, 1, h.m.Calls["setkeys"]-before, "many changed friends are still one write exchange, not one each")
}

// A lost batch write is truthful: it returns an error and writes none of the
// roster width or job records it held, so no partial state is ever visible.
func TestFriendSyncAFailedWriteWritesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := h.ctx

	_, _, _, err := h.st.SyncFriends(ctx, []FriendSpec{
		{Name: "a", Width: 1, Jobs: []FriendJob{{ID: "j1", State: JobReady}}},
		{Name: "b", Width: 1},
	})
	require.NoError(t, err)

	h.m.Fail = func(point string) error {
		if point == "setkeys" {
			return errors.New("the batch write was lost")
		}
		return nil
	}
	_, _, _, err = h.st.SyncFriends(ctx, []FriendSpec{
		{Name: "a", Width: 2, Jobs: []FriendJob{{ID: "j1", State: JobWorking}}},
		{Name: "b", Width: 1, Jobs: []FriendJob{{ID: "j2", State: JobDone, OK: true}}},
	})
	require.Error(t, err)

	// The roster width and every job record are as they were before the write.
	r, _, err := h.st.roster(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, r["a"].Width, "a failed write must not change a width")
	require.Equal(t, 1, r["b"].Width, "a failed write must not change b's width")
	got, ok, err := h.m.GetKey(ctx, friendJobsKey("a"))
	require.NoError(t, err)
	require.True(t, ok)
	require.JSONEq(t, `[{"id":"j1","state":"ready"}]`, got, "a failed write must not change a's jobs")
	_, ok, err = h.m.GetKey(ctx, friendJobsKey("b"))
	require.NoError(t, err)
	require.False(t, ok, "a failed write must not add b's jobs")
}
