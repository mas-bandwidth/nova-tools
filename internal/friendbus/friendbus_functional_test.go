//go:build functional

package friendbus

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func testBus(t *testing.T) (*Bus, *redis.Client, context.Context) {
	t.Helper()
	addr := testredis.Start(t)
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	b, err := New(Config{Prefix: "test", Redis: rdb})
	require.NoError(t, err)
	return b, rdb, ctx
}

func testSession() Session {
	return Session{ID: "session-1", Adapter: "native", Revision: "route-1", Capabilities: []string{"durable-delivery-id", "durable-receipt"}}
}

func TestPublishIsIdempotentAndAcceptanceIsSeparateFromBusinessCompletion(t *testing.T) {
	t.Parallel()
	b, rdb, ctx := testBus(t)
	require.NoError(t, b.Register(ctx, "recipient-a", testSession()))
	m := Message{Actor: "sender-a", Op: "send-1", From: "sender-a", To: []string{"recipient-a", "recipient-a"}, CC: []string{"recipient-c"}, Body: []byte("full note\n"), Date: "2026-10-03T12:00:00Z", Re: "parent"}
	id := OperationID(m.Actor, m.Op)
	first, err := b.Publish(ctx, m)
	require.NoError(t, err)
	require.Equal(t, PublishResult{ID: id, Queued: 1}, first)
	retry, err := b.Publish(ctx, m)
	require.NoError(t, err)
	require.Equal(t, PublishResult{ID: id, Queued: 0}, retry)
	conflict := m
	conflict.Body = []byte("different")
	_, err = b.Publish(ctx, conflict)
	require.ErrorIs(t, err, ErrConflict)
	ccLen, err := rdb.XLen(ctx, b.deliveryStream("recipient-c")).Result()
	require.NoError(t, err)
	require.Zero(t, ccLen, "Cc does not create an adapter delivery")

	deliveries, err := b.Claim(ctx, "recipient-a", "worker-a", 10, 0)
	require.NoError(t, err)
	require.Len(t, deliveries, 1)
	d := deliveries[0]
	require.Equal(t, "full note\n", string(d.Body))
	require.Equal(t, DeliveryKey(id, "recipient-a"), d.Key)
	require.Equal(t, "2026-10-03T12:00:00Z", d.Date)
	require.ErrorIs(t, b.Accept(ctx, "recipient-a", d, Acceptance{ReceiptID: "r-1", Adapter: "native", Session: "session-1", Revision: "route-1"}), ErrNotDurable)
	require.NoError(t, b.RecordFailure(ctx, "recipient-a", d, "adapter unavailable"))
	pending, err := rdb.XPending(ctx, b.deliveryStream("recipient-a"), b.group).Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), pending.Count, "transient adapter response does not acknowledge")
	require.NoError(t, b.Accept(ctx, "recipient-a", d, Acceptance{ReceiptID: "r-1", Adapter: "native", Session: "session-1", Revision: "route-1", Durable: true}))
	require.NoError(t, b.Accept(ctx, "recipient-a", d, Acceptance{ReceiptID: "r-1", Adapter: "native", Session: "session-1", Revision: "route-1", Durable: true}), "same durable receipt is idempotent")
	pending, err = rdb.XPending(ctx, b.deliveryStream("recipient-a"), b.group).Result()
	require.NoError(t, err)
	require.Zero(t, pending.Count, "only durable acceptance acknowledges the adapter PEL")
	_, found, err := b.GetSession(ctx, "recipient-a")
	require.NoError(t, err)
	require.True(t, found)
}

func TestPublishPreflightsEveryRecipientBeforeWriting(t *testing.T) {
	t.Parallel()
	b, rdb, ctx := testBus(t)
	m := Message{Actor: "sender-a", Op: "send-2", From: "sender-a", To: []string{"recipient-a", "recipient-z"}, Body: []byte("note")}
	require.NoError(t, rdb.Set(ctx, b.deliveryStream("recipient-z"), "wrong type", 0).Err())
	_, err := b.Publish(ctx, m)
	require.ErrorIs(t, err, ErrWrongType)
	sourceLen, err := rdb.XLen(ctx, b.sourceKey()).Result()
	require.NoError(t, err)
	require.Zero(t, sourceLen, "source is untouched when any target key has the wrong type")
	messageCount, err := rdb.HLen(ctx, b.messagesKey()).Result()
	require.NoError(t, err)
	require.Zero(t, messageCount, "dedup index is untouched")
	firstLen, err := rdb.XLen(ctx, b.deliveryStream("recipient-a")).Result()
	require.NoError(t, err)
	require.Zero(t, firstLen, "earlier recipient is untouched")
}

func TestReclaimRecoversPendingAndStaleSessionCannotAccept(t *testing.T) {
	t.Parallel()
	b, _, ctx := testBus(t)
	require.NoError(t, b.Register(ctx, "recipient-a", testSession()))
	m := Message{Actor: "sender-a", Op: "send-3", From: "sender-a", To: []string{"recipient-a"}, Body: []byte("note")}
	_, err := b.Publish(ctx, m)
	require.NoError(t, err)
	deliveries, err := b.Claim(ctx, "recipient-a", "worker-a", 10, 0)
	require.NoError(t, err)
	require.Len(t, deliveries, 1)
	deliveries, next, err := b.ReclaimPage(ctx, "recipient-a", "worker-b", 0, 10, "0-0")
	require.NoError(t, err)
	require.Equal(t, "0-0", next)
	require.Len(t, deliveries, 1)
	require.Equal(t, DeliveryKey(OperationID(m.Actor, m.Op), "recipient-a"), deliveries[0].Key)
	updated := testSession()
	updated.ID = "session-2"
	require.NoError(t, b.Register(ctx, "recipient-a", updated))
	err = b.Accept(ctx, "recipient-a", deliveries[0], Acceptance{ReceiptID: "r-old", Adapter: "native", Session: "session-1", Revision: "route-1", Durable: true})
	require.ErrorIs(t, err, ErrStaleSession)
	require.NoError(t, b.Accept(ctx, "recipient-a", deliveries[0], Acceptance{ReceiptID: "r-new", Adapter: "native", Session: "session-2", Revision: "route-1", Durable: true}))
}
