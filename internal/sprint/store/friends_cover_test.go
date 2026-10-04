package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noFriend is the refusal of a name the roster lacks; FriendNames is every
// friend of the roster in name order. The tests here reach both: no unit
// test did before (the stream coverage card of 2026-10-04).

// kvless is a Backend that keeps no records: embedding the interface itself
// hides the KV methods behind it, which is the seam rootKV reads and the
// store-without-friends refusal hangs on.
type kvless struct{ Backend }

// noFriend's refusal names every friend of the roster in sorted order, and
// none when the roster is empty; the remedy is friend sync.
func TestFriendsCoverNoFriendNamesTheRoster(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		roster map[string]friendEntry
		want   string
	}{
		{"the roster's names, sorted",
			map[string]friendEntry{"cat": {}, "amy": {Width: 3}, "bob": {}},
			"no friend zed on the friends table (friends: amy,bob,cat): its row is nova-config's friend row; run: nova-sprint friend sync"},
		{"an empty roster names none",
			map[string]friendEntry{},
			"no friend zed on the friends table (friends: none): its row is nova-config's friend row; run: nova-sprint friend sync"},
		{"one friend names her alone",
			map[string]friendEntry{"amy": {}},
			"no friend zed on the friends table (friends: amy): its row is nova-config's friend row; run: nova-sprint friend sync"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.EqualError(t, noFriend(tt.roster, "zed"), tt.want)
		})
	}
}

// The two calls noFriend guards — friend beat and friend down — refuse a
// name the roster lacks with its message and write nothing.
func TestFriendsCoverUnknownFriendIsRefusedAndWritesNothing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		do   func(*testing.T, *Store, context.Context) error
	}{
		{"friend beat", func(t *testing.T, st *Store, ctx context.Context) error {
			_, err := st.FriendBeat(ctx, "zed")
			return err
		}},
		{"friend down", func(t *testing.T, st *Store, ctx context.Context) error {
			return st.SetFriendHeld(ctx, "zed", true, "c", 0)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 3}})
			require.NoError(t, err)
			err = tt.do(t, h.st, h.ctx)
			assert.ErrorContains(t, err, "no friend zed on the friends table (friends: amy)")
			assert.ErrorContains(t, err, "run: nova-sprint friend sync")
			kv, err := h.st.rootKV()
			require.NoError(t, err)
			_, ok, err := kv.GetKey(h.ctx, friendBeatKey("zed"))
			require.NoError(t, err)
			assert.False(t, ok, "the refusal wrote no beat")
			names, err := h.st.FriendNames(h.ctx)
			require.NoError(t, err)
			assert.Equal(t, []string{"amy"}, names, "the refusal wrote no friend")
		})
	}
}

// FriendNames is every friend of the roster in name order, none when the
// roster holds no friend, and refuses when the store keeps no records.
func TestFriendsCoverFriendNamesListsTheRoster(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		store   func(t *testing.T) (*Store, context.Context)
		want    []string
		wantErr string
	}{
		{"every friend, by name", func(t *testing.T) (*Store, context.Context) {
			h := newHarness(t)
			_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "carla", Width: 1}, {Name: "amy", Width: 3}, {Name: "bob", Width: 2}})
			require.NoError(t, err)
			return h.st, h.ctx
		}, []string{"amy", "bob", "carla"}, ""},
		{"a fresh store has no friends", func(t *testing.T) (*Store, context.Context) {
			h := newHarness(t)
			return h.st, h.ctx
		}, nil, ""},
		{"a store that keeps no records refuses", func(t *testing.T) (*Store, context.Context) {
			return &Store{B: kvless{NewMem()}}, context.Background()
		}, nil, "this store keeps no beats"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, ctx := tt.store(t)
			names, err := st.FriendNames(ctx)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, names)
				return
			}
			require.NoError(t, err)
			if tt.want == nil {
				assert.Empty(t, names)
				return
			}
			assert.Equal(t, tt.want, names)
		})
	}
}
