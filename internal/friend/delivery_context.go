package friend

import (
	"context"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

type deliveryIDsKey struct{}

// DeliveredFilter is the caller contract for a Deliverer that durably records delivered bus IDs.
// Callers (such as the daemon's startBatch) filter already delivered messages before rendering
// an envelope, preventing duplicate delivery of overlapping messages.
type DeliveredFilter interface {
	Delivered(id string) bool
}

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

// FilterDelivered filters messages, returning only those that have not yet been delivered
// according to d, preventing duplicate delivery of overlapping messages.
func FilterDelivered(d Deliverer, msgs []bus.Message) []bus.Message {
	var filter DeliveredFilter
	under(d, func(del Deliverer) bool {
		if f, ok := del.(DeliveredFilter); ok {
			filter = f
			return true
		}
		return false
	})
	if filter == nil {
		return msgs
	}
	var out []bus.Message
	for _, m := range msgs {
		if !filter.Delivered(m.ID) {
			out = append(out, m)
		}
	}
	return out
}
