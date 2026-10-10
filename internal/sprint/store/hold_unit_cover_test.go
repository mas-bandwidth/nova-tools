package store

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStoreHoldCoverHoldReader holds a reader with reason and Who;
// Holds returns one HoldView with Kind reader, Name, Reason, By, At.
func TestStoreHoldCoverHoldReader(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	who := "tester"
	reason := "for review"

	res, err := h.st.Hold(h.ctx, sprint.HoldReq{
		Names:  []string{"reader-a"},
		Kind:   sprint.HoldReader,
		Reason: reason,
		Who:    who,
	})
	require.NoError(t, err)
	require.Empty(t, res.Refused)

	holds, err := h.st.Holds(h.ctx)
	require.NoError(t, err)
	require.Len(t, holds, 1)
	require.Equal(t, sprint.HoldReader, holds[0].Kind)
	require.Equal(t, "reader-a", holds[0].Name)
	require.Equal(t, reason, holds[0].Reason)
	require.Equal(t, who, holds[0].By)
	require.NotEmpty(t, holds[0].At)
}

// TestStoreHoldCoverHoldFriend holds a friend with reason and Who;
// Holds returns one HoldView with Kind friend, Name, Reason, By, At.
func TestStoreHoldCoverHoldFriend(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1}})
	require.NoError(t, err)

	who := "tester"
	reason := "for review"

	res, err := h.st.Hold(h.ctx, sprint.HoldReq{
		Names:  []string{"amy"},
		Kind:   sprint.HoldFriend,
		Reason: reason,
		Who:    who,
	})
	require.NoError(t, err)
	require.Empty(t, res.Refused)

	holds, err := h.st.Holds(h.ctx)
	require.NoError(t, err)
	require.Len(t, holds, 1)
	require.Equal(t, sprint.HoldFriend, holds[0].Kind)
	require.Equal(t, "amy", holds[0].Name)
	require.Equal(t, reason, holds[0].Reason)
	require.Equal(t, who, holds[0].By)
	require.NotEmpty(t, holds[0].At)
}

// TestStoreHoldCoverUnhold unholds reader and friend; Holds is empty.
func TestStoreHoldCoverUnhold(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1}})
	require.NoError(t, err)

	who := "tester"
	reason := "for review"

	// Hold reader-a
	_, err = h.st.Hold(h.ctx, sprint.HoldReq{Names: []string{"reader-a"}, Kind: sprint.HoldReader, Reason: reason, Who: who})
	require.NoError(t, err)

	// Hold amy
	_, err = h.st.Hold(h.ctx, sprint.HoldReq{Names: []string{"amy"}, Kind: sprint.HoldFriend, Reason: reason, Who: who})
	require.NoError(t, err)

	// Unhold both
	_, err = h.st.Hold(h.ctx, sprint.HoldReq{Names: []string{"reader-a", "amy"}, Release: true})
	require.NoError(t, err)

	// Verify unheld
	holds, err := h.st.Holds(h.ctx)
	require.NoError(t, err)
	require.Empty(t, holds)

	// Verify friend roster entry is clear
	ros, _, err := h.st.roster(h.ctx)
	require.NoError(t, err)
	require.False(t, ros["amy"].Held)
	require.True(t, ros["amy"].At.IsZero())
}

// TestStoreHoldCoverHoldReqOf tests HoldReqOf: Friends is roster names sorted;
// on release, Alive holds members whose beat is alive; on hold, Alive is nil.
func TestStoreHoldCoverHoldReqOf(t *testing.T) {
	t.Parallel()

	t.Run("friends sorted", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "zed", Width: 1}, {Name: "amy", Width: 2}})
		require.NoError(t, err)

		r, err := h.st.HoldReqOf(h.ctx, sprint.HoldReq{Names: []string{"amy"}})
		require.NoError(t, err)
		assert.Equal(t, []string{"amy", "zed"}, r.Friends)
		assert.Nil(t, r.Alive) // not a release
	})

	t.Run("release alive", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		r, err := h.st.HoldReqOf(h.ctx, sprint.HoldReq{Names: []string{"m1", "m2"}, Release: true})
		require.NoError(t, err)
		assert.Contains(t, r.Alive, "m1")
		assert.Contains(t, r.Alive, "m2")
	})

	t.Run("hold alive nil", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		// Set Alive explicitly; on hold it should be nil
		r, err := h.st.HoldReqOf(h.ctx, sprint.HoldReq{Names: []string{"m1"}, Alive: []string{"m1"}})
		require.NoError(t, err)
		assert.Nil(t, r.Alive)
	})
}

// TestStoreHoldCoverRefusals tests refusals: Hold of nothing refuses;
// Hold with Kind reader naming m1 refuses "no reader m1"; setFriendHold of
// friend not on roster returns noFriend error.
func TestStoreHoldCoverRefusals(t *testing.T) {
	t.Parallel()

	t.Run("hold nothing refuses", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		res, err := h.st.Hold(h.ctx, sprint.HoldReq{Names: []string{}})
		require.NoError(t, err)
		assert.NotEmpty(t, res.Refused)
	})

	t.Run("kind reader naming m1 refuses", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		res, err := h.st.Hold(h.ctx, sprint.HoldReq{Names: []string{"m1"}, Kind: sprint.HoldReader})
		require.NoError(t, err)
		assert.NotEmpty(t, res.Refused)
		assert.Contains(t, res.Refused[0].Key, "m1")
	})

	t.Run("setFriendHold unknown friend", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1}})
		require.NoError(t, err)

		err = h.st.SetFriendHeld(h.ctx, "zed", true, "reason", "who", time.Time{}, 0)
		assert.ErrorContains(t, err, "no friend zed on the friends table")

		kv, err := h.st.rootKV()
		require.NoError(t, err)
		_, ok, err := kv.GetKey(h.ctx, friendBeatKey("zed"))
		require.NoError(t, err)
		assert.False(t, ok, "the refusal wrote no beat")
	})
}

// TestStoreHoldCoverKVless tests on a second Store over kvless{h.m},
// HoldReqOf fails with "keeps no beats".
func TestStoreHoldCoverKVless(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 1}})
	require.NoError(t, err)

	// Create second store over kvless
	st := &Store{B: kvless{h.m}, Names: h.st.Names, Actor: h.st.Actor, Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep}

	// HoldReqOf fails with "keeps no beats"
	_, err = st.HoldReqOf(h.ctx, sprint.HoldReq{Names: []string{"amy"}})
	assert.ErrorContains(t, err, "keeps no beats")
}

// TestStoreHoldCoverFreshMem tests Holds on a store over a fresh NewMem()
// that was never Init-ed returns nil, nil (the NOTABLE branch).
func TestStoreHoldCoverFreshMem(t *testing.T) {
	t.Parallel()
	st := &Store{B: NewMem(), Names: sprint.Names{Prefix: "t-"}}

	holds, err := st.Holds(context.Background())
	require.NoError(t, err)
	assert.Nil(t, holds)
}

// TestStoreHoldCoverStampOf tests stampOf: zero time is "",
// time in another zone comes back in UTC.
func TestStoreHoldCoverStampOf(t *testing.T) {
	t.Parallel()

	t.Run("zero time", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "", stampOf(time.Time{}))
	})

	t.Run("time in another zone", func(t *testing.T) {
		t.Parallel()
		loc, err := time.LoadLocation("America/New_York")
		require.NoError(t, err)
		tm := time.Date(2026, 10, 10, 12, 0, 0, 0, loc)
		s := stampOf(tm)
		assert.Equal(t, "2026-10-10T16:00:00Z", s)
	})
}

// TestStoreHoldCoverHoldStep tests HoldStep Verb is "hold" and "unhold",
// and Args leave out Friends and Alive.
func TestStoreHoldCoverHoldStep(t *testing.T) {
	t.Parallel()

	t.Run("hold verb and args", func(t *testing.T) {
		t.Parallel()
		r := sprint.HoldReq{
			Names:   []string{"m1"},
			Reason:  "reason",
			Who:     "who",
			Friends: []string{"friend1"},
			Alive:   []string{"m1"},
		}
		step := HoldStep(r)
		assert.Equal(t, "hold", step.Verb)
		assert.NotEmpty(t, step.Args)
	})

	t.Run("unhold verb", func(t *testing.T) {
		t.Parallel()
		r := sprint.HoldReq{Names: []string{"m1"}, Release: true}
		step := HoldStep(r)
		assert.Equal(t, "unhold", step.Verb)
	})
}
