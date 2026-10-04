package bus2

import (
	"context"
	"errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestConsumerPendingPagesDoNotStealInteractiveWork(t *testing.T) {
	t.Parallel()
	b, f := rig(t, "ada", "bob")
	ctx := context.Background()
	_, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	interactive, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	page, _, err := b.PendingPage(ctx, "bob", "daemon", "", 10)
	require.NoError(t, err)
	assert.Empty(t, page)
	batch, err := b.RecvBatch(ctx, "bob", "daemon", 0, 10)
	require.NoError(t, err)
	assert.Empty(t, batch)
	f.Advance(ClaimAfter)
	batch, err = b.RecvBatch(ctx, "bob", "daemon", 0, 10)
	require.NoError(t, err)
	require.Len(t, batch, 1)
	assert.Equal(t, interactive.Entry, batch[0].Entry, "stale claim transfers by design")
	page, _, err = b.PendingPage(ctx, "bob", Consumer, "", 10)
	require.NoError(t, err)
	assert.Empty(t, page)
}

func TestConsumerPendingPagesCoverMoreThanOneThousandInOrder(t *testing.T) {
	t.Parallel()
	b, _ := rig(t, "ada", "bob")
	ctx := context.Background()
	for i := 0; i < 1205; i++ {
		_, err := b.Send(ctx, msg("ada", "bob"))
		require.NoError(t, err)
	}
	for _, n := range []int{1000, 205} {
		got, err := b.RecvBatch(ctx, "bob", "daemon", 0, n)
		require.NoError(t, err)
		require.Len(t, got, n)
	}
	var cursor string
	seen := map[string]bool{}
	for {
		page, next, err := b.PendingPage(ctx, "bob", "daemon", cursor, 137)
		require.NoError(t, err)
		if next == "" {
			break
		}
		for _, e := range page {
			assert.False(t, seen[e.Entry])
			if cursor != "" {
				assert.True(t, after(e.Entry, cursor))
			}
			seen[e.Entry] = true
			cursor = e.Entry
		}
		cursor = next
	}
	assert.Len(t, seen, 1205)
}

func TestBatchReceiveRejectsInvalidBoundsBeforeStoreCommands(t *testing.T) {
	t.Parallel()
	b, f := rig(t, "bob")
	for _, n := range []int{0, -1, 1001} {
		_, err := b.RecvBatch(context.Background(), "bob", "daemon", 0, n)
		require.Error(t, err)
	}
	_, _, err := b.PendingPage(context.Background(), "bob", "daemon", "bad", 10)
	require.Error(t, err)
	assert.Zero(t, f.Trips)
}

func TestFakePendingPageUsesNumericStreamIDOrder(t *testing.T) {
	t.Parallel()
	f := NewFake(start, "bob")
	f.groups["s/g"] = &fakeGroup{pending: map[string]time.Time{"9-0": start, "10-0": start, "10-2": start, "10-10": start}, owner: map[string]string{"9-0": "daemon", "10-0": "daemon", "10-2": "daemon", "10-10": "daemon"}}
	ids, err := f.PendingPage(context.Background(), "s", "g", "daemon", "9-0", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"10-0", "10-2", "10-10"}, ids)
}

func TestPendingPageAdvancesPastMissingBodies(t *testing.T) {
	t.Parallel()
	b, f := rig(t, "ada", "bob")
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, err := b.Send(ctx, msg("ada", "bob"))
		require.NoError(t, err)
	}
	got, err := b.RecvBatch(ctx, "bob", "daemon", 0, 3)
	require.NoError(t, err)
	require.Len(t, got, 3)
	f.streams[StreamOf("bob")] = f.streams[StreamOf("bob")][2:]
	empty, next, err := b.PendingPage(ctx, "bob", "daemon", "", 2)
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.Equal(t, got[1].Entry, next)
	page, last, err := b.PendingPage(ctx, "bob", "daemon", next, 2)
	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, got[2].Entry, last)
	_, end, err := b.PendingPage(ctx, "bob", "daemon", last, 2)
	require.NoError(t, err)
	assert.Empty(t, end)
	f.Fail = errors.New("store unavailable")
	_, failed, err := b.PendingPage(ctx, "bob", "daemon", last, 2)
	require.Error(t, err)
	assert.Empty(t, failed, "errors never publish a successful cursor")
}
