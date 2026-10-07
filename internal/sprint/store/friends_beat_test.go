package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The beat contract (docs/FRIENDS.md): a beat that does not name the running list,
// the working count or the queue keeps the last beat's; a beat that names one
// replaces it, and a named empty `--running` clears it. A bare keepalive therefore
// never erases what the store knows about a friend's running cards.
func TestAFriendBeatLeavesWhatItDoesNotName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 3}})
	require.NoError(t, err)

	working, queue := 5, 2
	_, err = h.st.FriendBeatReport(h.ctx, "amy", sprint.FriendReport{Running: []string{"s1-1.w1"}, Working: &working, Queue: &queue}, nil)
	require.NoError(t, err)

	// a bare beat: the list and the counts stand
	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)
	b, err := h.st.FriendBeatOf(h.ctx, "amy")
	require.NoError(t, err)
	require.NotNil(t, b.Friend)
	assert.Equal(t, []string{"s1-1.w1"}, b.Friend.Running, "a beat without --running keeps the list")
	require.NotNil(t, b.Friend.Working)
	assert.Equal(t, 5, *b.Friend.Working, "a beat without --working keeps the count")
	require.NotNil(t, b.Friend.Queue)
	assert.Equal(t, 2, *b.Friend.Queue, "a beat without --queue keeps the count")

	// a named list replaces the last one
	_, err = h.st.FriendBeatReport(h.ctx, "amy", sprint.FriendReport{Running: []string{"s1-2.w1"}}, nil)
	require.NoError(t, err)
	b, err = h.st.FriendBeatOf(h.ctx, "amy")
	require.NoError(t, err)
	require.NotNil(t, b.Friend)
	assert.Equal(t, []string{"s1-2.w1"}, b.Friend.Running)
	require.NotNil(t, b.Friend.Working)
	assert.Equal(t, 5, *b.Friend.Working, "a beat with --running alone keeps the counts")

	// a named empty --running clears the list and leaves the counts
	_, err = h.st.FriendBeatReport(h.ctx, "amy", sprint.FriendReport{Running: []string{}}, nil)
	require.NoError(t, err)
	b, err = h.st.FriendBeatOf(h.ctx, "amy")
	require.NoError(t, err)
	require.NotNil(t, b.Friend)
	assert.Empty(t, b.Friend.Running, "a named empty --running clears the list")
	require.NotNil(t, b.Friend.Working)
	assert.Equal(t, 5, *b.Friend.Working)
	require.NotNil(t, b.Friend.Queue)
	assert.Equal(t, 2, *b.Friend.Queue)
}

// A beat with no report word at all keeps no report the first time, and a beat
// without `--until` withdraws the down word: the carried words are the running
// list, the working count, the queue and the width alone (docs/FRIENDS.md, the
// beat contract).
func TestAFriendBeatCarriesNoWordItDidNotName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 3}})
	require.NoError(t, err)

	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)
	b, err := h.st.FriendBeatOf(h.ctx, "amy")
	require.NoError(t, err)
	assert.Nil(t, b.Friend, "a beat that reports nothing carries no report")

	// a beat that says she is down: the next bare beat withdraws the word, and the
	// running list it named stands
	until := h.now.Add(10 * time.Minute)
	_, err = h.st.FriendBeatReport(h.ctx, "amy", sprint.FriendReport{Running: []string{"s1-1.w1"}, Until: until, Reason: "out of credits"}, nil)
	require.NoError(t, err)
	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)
	b, err = h.st.FriendBeatOf(h.ctx, "amy")
	require.NoError(t, err)
	require.NotNil(t, b.Friend)
	assert.True(t, b.Friend.Until.IsZero(), "a beat without --until withdraws the down word")
	assert.Empty(t, b.Friend.Reason)
	assert.Equal(t, []string{"s1-1.w1"}, b.Friend.Running)
}
