//go:build functional

package bus2

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// live is a bus over a throwaway redis-server whose roster names ada and bob,
// the shape internal/bus2's fake imitates; every rule the unit tests pin on
// the fake runs here once against the real commands.
func live(t *testing.T) (*Bus, *redis.Client, context.Context) {
	t.Helper()
	addr := testredis.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	require.NoError(t, c.SAdd(ctx, friendsKey, "ada", "bob").Err())
	require.NoError(t, c.SAdd(ctx, machinesKey, "m1").Err())
	return &Bus{Store: Redis{C: c}}, c, ctx
}

func TestRedisStoreRunsTheWholeLoop(t *testing.T) {
	t.Parallel()
	b, c, ctx := live(t)

	names, err := b.Names(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"ada", "bob", "m1"}, names)

	m1, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, CC: []string{"m1"}, Subject: "one", Body: "first\n"})
	require.NoError(t, err)
	m2, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "two", Body: "second\n", Re: m1.ID})
	require.NoError(t, err)
	assert.Less(t, m1.ID, m2.ID)
	assert.WithinDuration(t, time.Now(), m1.At, time.Minute, "at is the server's time")
	for _, k := range []string{StreamOf("bob"), StreamOf("m1"), LogKey} {
		n, err := c.XLen(ctx, k).Result()
		require.NoError(t, err)
		assert.EqualValues(t, map[string]int64{StreamOf("bob"): 2, StreamOf("m1"): 1, LogKey: 2}[k], n, k)
	}

	// peek before any group exists: everything new
	pending, fresh, err := b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Empty(t, pending)
	assert.Len(t, fresh, 2)

	e, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m1.ID, e.Message().ID)
	assert.Equal(t, "first\n", e.Message().Body)

	// a second reader at once: m1 is held (idle under ClaimAfter), so m2 comes
	e2, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m2.ID, e2.Message().ID, "a held message is not handed out twice (Bus2.tla HeldStaysHeld)")

	pending, fresh, err = b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Len(t, pending, 2)
	assert.Empty(t, fresh)

	_, ok, err = b.Recv(ctx, "bob", 100*time.Millisecond)
	require.NoError(t, err)
	assert.False(t, ok, "a block that runs out is no error, and nothing held is handed out")

	acked, err := b.Ack(ctx, "bob", []string{m1.ID, "NOPE"})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{m1.ID: true, "NOPE": false}, acked)
	acked, err = b.Ack(ctx, "bob", []string{m1.ID})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{m1.ID: false}, acked, "idempotent")
	done, err := b.AckEntry(ctx, "bob", e2.Entry)
	require.NoError(t, err)
	assert.True(t, done)

	// the reader of a message died: the store's idle clock says so, and the
	// store's own command hands it out again (the idle is set by hand, as
	// XAUTOCLAIM reads it, so no test waits a minute)
	m3, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "three", Body: "third\n"})
	require.NoError(t, err)
	e3, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m3.ID, e3.Message().ID)
	require.NoError(t, c.XClaim(ctx, &redis.XClaimArgs{Stream: StreamOf("bob"), Group: "bob", Consumer: Consumer, MinIdle: 0, Messages: []string{e3.Entry}}).Err())
	require.NoError(t, c.Do(ctx, "XCLAIM", StreamOf("bob"), "bob", Consumer, "0", e3.Entry, "IDLE", (ClaimAfter+time.Second).Milliseconds()).Err())
	e4, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m3.ID, e4.Message().ID, "a message its reader lost comes back after ClaimAfter (Bus2.tla PendingBeforeNew)")

	got, err := b.Log(ctx)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, m1.ID, got[0].Message().ID)
	assert.Equal(t, []string{"m1"}, got[0].Message().CC)
	assert.Equal(t, m1.ID, got[1].Message().Re)

	// nothing was deleted
	for _, k := range []string{StreamOf("bob"), StreamOf("m1"), LogKey} {
		n, err := c.XLen(ctx, k).Result()
		require.NoError(t, err)
		assert.NotZero(t, n, k)
	}
}

func TestRedisStoreRefusesWhatTheFakeRefuses(t *testing.T) {
	t.Parallel()
	b, c, ctx := live(t)
	_, err := b.Send(ctx, Message{From: "ada", To: []string{"zed"}, Subject: "s", Body: "x"})
	var r *Refusal
	require.ErrorAs(t, err, &r)
	assert.Contains(t, err.Error(), "zed is no known name")
	pending, fresh, err := b.Peek(ctx, "nobody")
	require.NoError(t, err)
	assert.Empty(t, pending)
	assert.Empty(t, fresh)
	_, _, err = b.Recv(ctx, "nobody", 0)
	require.ErrorAs(t, err, &r)
	n, err := c.Exists(ctx, StreamOf("nobody")).Result()
	require.NoError(t, err)
	assert.Zero(t, n, "recv of an unknown name makes no stream")
	acked, err := b.Ack(ctx, "nobody", []string{"X"})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"X": false}, acked)
}
