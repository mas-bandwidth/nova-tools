package store

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's beat leaves a running list, a working count and a queue count it
// does not name (docs/FRIENDS.md, a beat leaves a field it does not name). A nil
// list or count is absent. An empty running list is --running given empty, and
// it clears. A count named, including 0, replaces that count.

func TestABeatLeavesAFieldItDoesNotName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 4}})
	require.NoError(t, err)

	_, proof, err := h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{Running: []string{"s1-1.w1", "s1-2.w1"}}, nil, sprint.BeatWords{})
	require.NoError(t, err)
	assert.Equal(t, "running", proof.Set)
	_, proof, err = h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{}, nil, sprint.BeatWords{})
	require.NoError(t, err)
	assert.Equal(t, "-", proof.Set)
	assert.Equal(t, []string{"s1-1.w1", "s1-2.w1"}, friendBeatReportOf(t, h, "amy").Running, "a bare beat leaves the running list")

	_, err = h.st.FriendBeatReport(h.ctx, "amy", sprint.FriendReport{Running: []string{"s1-3.w1"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"s1-3.w1"}, friendBeatReportOf(t, h, "amy").Running, "a list replaces the list")

	five, three := 5, 3
	_, proof, err = h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{Working: &five, Queue: &three}, nil, sprint.BeatWords{})
	require.NoError(t, err)
	assert.Equal(t, "working,queue", proof.Set)
	got := friendBeatReportOf(t, h, "amy")
	assert.Equal(t, []string{"s1-3.w1"}, got.Running, "naming a count does not clear the list")
	require.NotNil(t, got.Working)
	require.NotNil(t, got.Queue)
	assert.Equal(t, 5, *got.Working)
	assert.Equal(t, 3, *got.Queue)

	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)
	got = friendBeatReportOf(t, h, "amy")
	assert.Equal(t, 5, *got.Working, "working left off stays 5, and is not written as zero")
	assert.Equal(t, 3, *got.Queue, "queue left off stays")
	assert.Equal(t, []string{"s1-3.w1"}, got.Running)

	_, proof, err = h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{Running: []string{}}, nil, sprint.BeatWords{})
	require.NoError(t, err)
	assert.Equal(t, "running", proof.Set, "--running '' names the field and clears it")
	got = friendBeatReportOf(t, h, "amy")
	assert.Empty(t, got.Running)
	assert.Equal(t, 5, *got.Working, "clearing the list leaves the counts")
	assert.Equal(t, 3, *got.Queue)

	zero := 0
	_, proof, err = h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{Working: &zero}, nil, sprint.BeatWords{})
	require.NoError(t, err)
	assert.Equal(t, "working", proof.Set)
	got = friendBeatReportOf(t, h, "amy")
	require.NotNil(t, got.Working)
	assert.Equal(t, 0, *got.Working, "a named zero replaces the count")
	assert.Equal(t, 3, *got.Queue)
	assert.Empty(t, got.Running)
}

// The daemon's keepalive often names activity and not the running list, and a
// down beat names until and a zero working count. Neither erases the list. A
// later beat that does not say down withdraws the down word and still leaves
// the list.
func TestAKeepaliveAndADownBeatLeaveTheRunningList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 4}})
	require.NoError(t, err)
	_, err = h.st.FriendBeatReport(h.ctx, "amy", sprint.FriendReport{Running: []string{"s1-1.w1"}}, nil)
	require.NoError(t, err)

	active := h.now
	_, proof, err := h.st.FriendBeatProof(h.ctx, "amy", sprint.FriendReport{Active: active}, nil, sprint.BeatWords{})
	require.NoError(t, err)
	assert.Equal(t, "-", proof.Set)
	got := friendBeatReportOf(t, h, "amy")
	assert.Equal(t, []string{"s1-1.w1"}, got.Running)
	assert.True(t, got.Active.Equal(active))

	zero := 0
	until := h.now.Add(time.Hour)
	_, err = h.st.FriendBeatReport(h.ctx, "amy", sprint.FriendReport{Working: &zero, Until: until, Reason: "limit", Active: active}, nil)
	require.NoError(t, err)
	got = friendBeatReportOf(t, h, "amy")
	assert.Equal(t, []string{"s1-1.w1"}, got.Running, "a down beat that does not name running leaves the list")
	assert.True(t, got.Until.Equal(until))
	assert.Equal(t, "limit", got.Reason)

	_, err = h.st.FriendBeat(h.ctx, "amy")
	require.NoError(t, err)
	got = friendBeatReportOf(t, h, "amy")
	assert.Equal(t, []string{"s1-1.w1"}, got.Running)
	assert.True(t, got.Until.IsZero(), "a beat that does not say down withdraws the down word")
	assert.Empty(t, got.Reason)
	require.NotNil(t, got.Working)
	assert.Equal(t, 0, *got.Working, "the zero the down beat named stays until a beat names another")
}

func friendBeatReportOf(t *testing.T, h *harness, friend string) sprint.FriendReport {
	t.Helper()
	b, err := h.st.FriendBeatOf(h.ctx, friend)
	require.NoError(t, err)
	if b.Friend == nil {
		return sprint.FriendReport{}
	}
	return *b.Friend
}
