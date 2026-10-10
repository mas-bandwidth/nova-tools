package bus

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One store mutation owns both the comparison and time (SPEC-BUS.md, message-receipts).
func TestMarkReceiptsIsOneAtomicStoreTimeTransition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	f := NewFake(now, "ada")
	b := &Bus{Store: f}
	before := f.Trips
	_, err := b.Stamp(ctx, "ada", Delivered, "a", "b")
	require.NoError(t, err)
	assert.Equal(t, 1, f.Trips-before, "no read before the store mutation")
	f.Advance(time.Minute)
	var wg sync.WaitGroup
	for _, state := range []string{Read, Acted, Delivered, Read, Acted} {
		wg.Add(1)
		go func(state string) {
			defer wg.Done()
			_, err := b.Stamp(ctx, "ada", state, "a", "b")
			assert.NoError(t, err)
		}(state)
	}
	wg.Wait()
	got, _, err := b.Stages(ctx, "ada", "a", "b")
	require.NoError(t, err)
	for _, receipt := range got {
		assert.Equal(t, Acted, receipt.State)
		assert.Equal(t, now.Add(time.Minute), receipt.At)
	}
}

// A reply names an act only once delivery is stored (SPEC-BUS.md, message-receipts).
func TestReceiptReplyNeverActsBeforeDelivered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := NewFake(time.Unix(1, 0), "ada", "bob")
	b := &Bus{Store: f}
	m, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "one", Body: "one"})
	require.NoError(t, err)
	_, err = b.Send(ctx, Message{From: "bob", To: []string{"ada"}, Subject: "reply", Re: m.ID, Body: "from log"})
	require.NoError(t, err)
	got, _, err := b.Stages(ctx, "bob", m.ID)
	require.NoError(t, err)
	assert.Empty(t, got[0].State)
}
