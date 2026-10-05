package bus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// namedFake is the fake whose set `friends` a sender can add to, as Redis's SADD.
type namedFake struct {
	*Fake
	adds [][]string
}

func (f *namedFake) AddFriends(_ context.Context, names ...string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.trip(); err != nil {
		return nil, err
	}
	f.adds = append(f.adds, names)
	f.names = append(f.names, names...)
	f.Friends = append(f.Friends, names...)
	return names, nil
}

// KnowFriends makes a friend row the bus store's roster predates a name: added once, only
// what is missing, and the send to her then lands; a name the roster holds, as a friend or
// a machine, is read and never written; a name that is no name is refused before a trip.
func TestKnowFriendsAddsOnlyTheMissingRowsAndTheSendLands(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := &namedFake{Fake: NewFake(time.Date(2026, 10, 5, 17, 0, 0, 0, time.UTC), "coord", "m1")}
	f.Friends = []string{"coord"}
	b := &Bus{Store: f}

	_, err := b.Send(ctx, Message{From: "coord", To: []string{"rowan-space"}, Subject: "s", Body: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rowan-space is no known name", "the stale roster refuses her")

	added, err := KnowFriends(ctx, f, "rowan-space", "coord", "m1", "rowan-space")
	require.NoError(t, err)
	assert.Equal(t, []string{"rowan-space"}, added)
	assert.Equal(t, [][]string{{"rowan-space"}}, f.adds, "only the missing name, once")
	_, err = b.Send(ctx, Message{From: "coord", To: []string{"rowan-space"}, Subject: "s", Body: "x"})
	require.NoError(t, err)
	assert.Equal(t, 1, f.Len(StreamOf("rowan-space")))
	e, ok, err := b.Recv(ctx, "rowan-space", 0)
	require.NoError(t, err)
	require.True(t, ok, "her recv takes it")
	assert.Equal(t, "coord", e.Message().From)

	trips := f.Trips
	added, err = KnowFriends(ctx, f, "rowan-space")
	require.NoError(t, err)
	assert.Empty(t, added)
	assert.Equal(t, trips+1, f.Trips, "a known name is one read and no write")
	assert.Len(t, f.adds, 1)

	trips = f.Trips
	_, err = KnowFriends(ctx, f, "Bad Name")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not lowercase letters, digits and hyphens")
	assert.Equal(t, trips, f.Trips, "refused before a trip")

	// a store that cannot add adds nothing, and the send is checked as before
	plain := NewFake(time.Date(2026, 10, 5, 17, 0, 0, 0, time.UTC), "coord")
	added, err = KnowFriends(ctx, plain, "rowan-space")
	require.NoError(t, err)
	assert.Empty(t, added)
	assert.Zero(t, plain.Trips)

	// the store down: the read's error, nothing added
	f.Fail = assert.AnError
	_, err = KnowFriends(ctx, f, "zed")
	assert.ErrorIs(t, err, assert.AnError)
	assert.Len(t, f.adds, 1)
}
