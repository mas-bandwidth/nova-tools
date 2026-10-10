package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The friends' reads and the health clear, on the in-memory twin: the six
// internal/sprint/store/friends.go functions no unit test reached (RestrictionWhy,
// FriendSpecs, HealthClearStep, FriendHealthClear, FriendSpecOf and FriendBeats),
// each read through the roster and the friends' records rootKV keeps. Every test
// here opens with t.Parallel(), touches no subprocess, no network and no live
// store, and is selected by -run TestStoreFriendsCover.

// RestrictionWhy is sprint.FriendRestrictionWhy over the spec's own comma lists:
// no list is no restriction, a streams glob either matches or names the stream and
// the list, and a kinds list either holds the kind or names it and the list.
func TestStoreFriendsCoverRestrictionWhy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		streams      string
		kinds        string
		stream, kind string
		want         string
	}{
		{"no restriction allows anything", "", "", "any-stream", "any-kind", ""},
		{"a streams glob that matches", "security*", "", "security-a", "", ""},
		{"a streams glob that does not match names the stream and the list", "security*", "", "build-a", "",
			`stream "build-a" is outside this friend's streams restriction (security*)`},
		{"a kinds list that holds the kind", "", "fix-red, review", "any-stream", "review", ""},
		{"a kinds list that does not hold the kind names it and the list", "", "fix-red, review", "any-stream", "build",
			`KIND "build" is outside this friend's kinds restriction (fix-red,review)`},
		{"a matching stream still refuses a kind the list lacks", "sec*", "fix", "sec-a", "review",
			`KIND "review" is outside this friend's kinds restriction (fix)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := FriendSpec{Streams: tt.streams, Kinds: tt.kinds}
			assert.Equal(t, tt.want, s.RestrictionWhy(tt.stream, tt.kind))
		})
	}
}

// specOfFull is a spec with every field set, for the round trips.
func specOfFull() FriendSpec {
	return FriendSpec{Name: "amy", Width: 3, Class: "a,b", Mode: "one-shot", ConfigDir: "/etc/amy",
		TokenCap: 4096, TokenCapSet: true, Roles: "builder,reader", Billing: "api",
		Streams: "s1,s2", Kinds: "k1,k2"}
}

// FriendSpecOf reads back every field friend sync wrote of one friend, and refuses
// a name the roster lacks with noFriend's message naming the roster.
func TestStoreFriendsCoverSpecOfRoundTripsEveryField(t *testing.T) {
	t.Parallel()

	t.Run("every field comes back as synced", func(t *testing.T) {
		h := newHarness(t)
		spec := specOfFull()
		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{spec})
		require.NoError(t, err)
		got, err := h.st.FriendSpecOf(h.ctx, "amy")
		require.NoError(t, err)
		assert.Equal(t, spec, got)
	})

	t.Run("an unknown name names the roster", func(t *testing.T) {
		h := newHarness(t)
		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 3}})
		require.NoError(t, err)
		_, err = h.st.FriendSpecOf(h.ctx, "zed")
		require.Error(t, err)
		assert.ErrorContains(t, err, "no friend zed on the friends table (friends: amy)")
		assert.ErrorContains(t, err, "run: nova-sprint friend sync")
	})
}

// FriendSpecs reads the whole synced roster in name order, carrying each friend's
// width, class, mode, config dir, token cap and its set flag, streams and kinds;
// an empty roster is empty with no error, and an unreadable friends record is
// refused with the friend sync remedy.
func TestStoreFriendsCoverSpecsReadsTheRoster(t *testing.T) {
	t.Parallel()

	t.Run("two friends synced out of order come back in name order with their fields", func(t *testing.T) {
		h := newHarness(t)
		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{
			{Name: "carla", Width: 2, Class: "b", Mode: "one-shot", ConfigDir: "/etc/carla", TokenCap: 0, TokenCapSet: true,
				Roles: "reader", Billing: "subscription", Streams: "s2", Kinds: "k2"},
			{Name: "amy", Width: 4, Class: "a", Mode: "batch", ConfigDir: "/etc/amy", TokenCap: 4096, TokenCapSet: true,
				Roles: "builder", Billing: "api", Streams: "s1", Kinds: "k1"},
		})
		require.NoError(t, err)
		got, err := h.st.FriendSpecs(h.ctx)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "amy", got[0].Name)
		assert.Equal(t, 4, got[0].Width)
		assert.Equal(t, "a", got[0].Class)
		assert.Equal(t, "batch", got[0].Mode)
		assert.Equal(t, "/etc/amy", got[0].ConfigDir)
		assert.Equal(t, int64(4096), got[0].TokenCap)
		assert.True(t, got[0].TokenCapSet)
		assert.Equal(t, "s1", got[0].Streams)
		assert.Equal(t, "k1", got[0].Kinds)
		assert.Equal(t, "carla", got[1].Name)
		assert.Equal(t, 2, got[1].Width)
		assert.Equal(t, "b", got[1].Class)
		assert.Equal(t, "one-shot", got[1].Mode)
		assert.Equal(t, "/etc/carla", got[1].ConfigDir)
		assert.Equal(t, int64(0), got[1].TokenCap)
		assert.True(t, got[1].TokenCapSet)
		assert.Equal(t, "s2", got[1].Streams)
		assert.Equal(t, "k2", got[1].Kinds)
	})

	t.Run("an empty roster is empty with no error", func(t *testing.T) {
		h := newHarness(t)
		got, err := h.st.FriendSpecs(h.ctx)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("an unreadable friends record names the friend sync remedy", func(t *testing.T) {
		h := newHarness(t)
		require.NoError(t, h.m.SetKey(h.ctx, "friends", "{"))
		got, err := h.st.FriendSpecs(h.ctx)
		require.Error(t, err)
		assert.ErrorContains(t, err, "the friends record cannot be read")
		assert.ErrorContains(t, err, "run: nova-sprint friend sync")
		assert.Nil(t, got)
	})
}

// FriendBeats is every friend of the roster with her last beat: one who beat
// carries it, one who never did carries a zero beat, an empty roster is an empty
// map with no error, and an unreadable beat record is a zero beat with no error.
// A store that keeps no records answers an empty map and rootKV's keeps-no-beats
// refusal, as FriendNames does.
func TestStoreFriendsCoverFriendBeats(t *testing.T) {
	t.Parallel()

	t.Run("one friend beat and one never did", func(t *testing.T) {
		h := newHarness(t)
		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2}, {Name: "bob", Width: 2}})
		require.NoError(t, err)
		_, err = h.st.FriendBeat(h.ctx, "amy")
		require.NoError(t, err)
		got, err := h.st.FriendBeats(h.ctx)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, t0, got["amy"].At, "the friend who beat carries her beat")
		assert.True(t, got["bob"].At.IsZero(), "the friend who never beat carries a zero beat")
	})

	t.Run("an empty roster is an empty map", func(t *testing.T) {
		h := newHarness(t)
		got, err := h.st.FriendBeats(h.ctx)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("a store that keeps no records answers an empty map", func(t *testing.T) {
		st := &Store{B: kvless{NewMem()}}
		got, err := st.FriendBeats(context.Background())
		require.ErrorContains(t, err, "this store keeps no beats")
		assert.Empty(t, got)
	})

	t.Run("an unreadable beat record is a zero beat", func(t *testing.T) {
		h := newHarness(t)
		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2}})
		require.NoError(t, err)
		require.NoError(t, h.m.SetKey(h.ctx, friendBeatKey("amy"), "{"))
		got, err := h.st.FriendBeats(h.ctx)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.True(t, got["amy"].At.IsZero(), "an unreadable record is no beat")
	})
}

// FriendHealthClear: a dry clear reports the observation that stood and writes
// nothing; a real clear by the seat's holder removes it and returns the status her
// evidence now gives; a clear by anyone but the holder, and of an unknown friend,
// refuse dry and real; a friend with no observation clears with had false.
func TestStoreFriendsCoverHealthClear(t *testing.T) {
	t.Parallel()

	// observe syncs amy and has tester record one up observation at the harness
	// clock: the record every clear below rests on.
	observe := func(t *testing.T) (*harness, sprint.FriendHealth) {
		t.Helper()
		h := newHarness(t)
		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2}})
		require.NoError(t, err)
		rec, status, replayed, err := h.health("amy", "tester", sprint.Up, h.now, 1)
		require.NoError(t, err)
		require.False(t, replayed)
		require.Equal(t, sprint.Up, status)
		return h, rec
	}

	t.Run("a dry clear reports the record and writes nothing", func(t *testing.T) {
		h, rec := observe(t)
		prev, had, status, err := h.st.FriendHealthClear(h.ctx, "amy", "tester", true, "")
		require.NoError(t, err)
		assert.True(t, had)
		assert.Equal(t, rec, prev)
		assert.Equal(t, sprint.Down, status, "the dry clear reports the status the removal would leave")
		assert.Equal(t, sprint.Up, h.friendStatus("amy"), "the observation still stands")
		_, ok, err := h.m.GetKey(h.ctx, friendHealthKey("amy"))
		require.NoError(t, err)
		assert.True(t, ok, "a dry clear leaves the record")
	})

	t.Run("a real clear by the seat removes it and returns her status", func(t *testing.T) {
		h, rec := observe(t)
		prev, had, status, err := h.st.FriendHealthClear(h.ctx, "amy", "tester", false, "")
		require.NoError(t, err)
		assert.True(t, had)
		assert.Equal(t, rec, prev)
		assert.Equal(t, sprint.Down, status, "with the observation gone she is down on no session evidence")
		_, ok, err := h.m.GetKey(h.ctx, friendHealthKey("amy"))
		require.NoError(t, err)
		assert.False(t, ok, "the record is removed")
		assert.Equal(t, sprint.Down, h.friendStatus("amy"))
	})

	t.Run("a clear by anyone but the seat refuses and writes nothing", func(t *testing.T) {
		for _, dry := range []bool{true, false} {
			h, _ := observe(t)
			_, _, _, err := h.st.FriendHealthClear(h.ctx, "amy", "stella", dry, "")
			require.Error(t, err)
			assert.ErrorContains(t, err, "friend health is the seat's: tester, not stella")
			_, ok, err := h.m.GetKey(h.ctx, friendHealthKey("amy"))
			require.NoError(t, err)
			assert.True(t, ok, "a refused clear wrote nothing")
		}
	})

	t.Run("an unknown friend refuses dry and real", func(t *testing.T) {
		for _, dry := range []bool{true, false} {
			h, _ := observe(t)
			_, _, _, err := h.st.FriendHealthClear(h.ctx, "zed", "tester", dry, "")
			require.Error(t, err)
			assert.ErrorContains(t, err, "no friend zed on the friends table")
		}
	})

	t.Run("a friend with no record clears with had false", func(t *testing.T) {
		h := newHarness(t)
		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2}})
		require.NoError(t, err)
		prev, had, status, err := h.st.FriendHealthClear(h.ctx, "amy", "tester", false, "")
		require.NoError(t, err)
		assert.False(t, had)
		assert.Equal(t, sprint.FriendHealth{}, prev)
		assert.Equal(t, sprint.Down, status)
	})
}

// HealthClearStep is the coordinator's verb: friend health, named.
func TestStoreFriendsCoverHealthClearStep(t *testing.T) {
	t.Parallel()
	step := HealthClearStep(sprint.HealthClearReq{Friend: "amy", Who: "tester", Known: true})
	assert.Equal(t, "friend health", step.Verb)
	assert.True(t, step.Named)
}
