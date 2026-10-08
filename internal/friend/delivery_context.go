package friend

import (
	"context"
)

type deliveryIDsKey struct{}

// WithDeliveryIDs attaches trusted stream entry IDs or message IDs to ctx for delivery.
func WithDeliveryIDs(ctx context.Context, ids []string) context.Context {
	return context.WithValue(ctx, deliveryIDsKey{}, ids)
}

// DeliveryIDsFromContext retrieves trusted delivery IDs from ctx.
// It reports (ids, true) when structured delivery metadata is present in ctx,
// even if the slice is explicitly empty ([]string{}).
func DeliveryIDsFromContext(ctx context.Context) ([]string, bool) {
	if ctx == nil {
		return nil, false
	}
	ids, ok := ctx.Value(deliveryIDsKey{}).([]string)
	return ids, ok
}
