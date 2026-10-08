package friend

import (
	"context"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
)

func TestDeliveryContext(t *testing.T) {
	t.Parallel()

	// Nil context or context without metadata returns false
	ids, ok := DeliveryIDsFromContext(nil)
	assert.False(t, ok)
	assert.Nil(t, ids)

	ids, ok = DeliveryIDsFromContext(context.Background())
	assert.False(t, ok)
	assert.Nil(t, ids)

	// Context with populated IDs
	ctx := WithDeliveryIDs(context.Background(), []string{"01M4A", "01M4B"})
	ids, ok = DeliveryIDsFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, []string{"01M4A", "01M4B"}, ids)

	// Explicit empty set is preserved with ok = true
	ctxEmpty := WithDeliveryIDs(context.Background(), []string{})
	ids, ok = DeliveryIDsFromContext(ctxEmpty)
	assert.True(t, ok)
	assert.Empty(t, ids)
}

type mockDeliveredFilter struct {
	delivered map[string]bool
	err       error
}

func (m *mockDeliveredFilter) Deliver(ctx context.Context, text string) (int, error) {
	return 0, nil
}

func (m *mockDeliveredFilter) Delivered(id string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	return m.delivered[id], nil
}

func TestFilterDelivered(t *testing.T) {
	t.Parallel()

	msgs := []bus.Message{
		{ID: "01M4A"},
		{ID: "01M4B"},
		{ID: "01M4C"},
	}

	// DeliveredFilter filters delivered messages
	filter := &mockDeliveredFilter{
		delivered: map[string]bool{"01M4A": true, "01M4B": true},
	}
	kept := FilterDelivered(filter, msgs)
	assert.Equal(t, []bus.Message{{ID: "01M4C"}}, kept)

	// DeliveredFilter with error fails closed, preserving messages
	errFilter := &mockDeliveredFilter{
		err: errors.New("temporary filter outage"),
	}
	keptOnErr := FilterDelivered(errFilter, msgs)
	assert.Equal(t, msgs, keptOnErr, "filter error preserves all messages pending")
}
