package friend

import (
	"context"
	"testing"

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
