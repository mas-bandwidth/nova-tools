package task

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Width is the slot accounting of one friend (spec 2.2, 4.2). Leased is
// ZCARD(starting) + ZCARD(living); Free is desired minus leased. starting and
// living are global to the consumer, so their ZCARDs already sum over every
// open sprint.
type Width struct {
	Desired  int
	Starting int
	Living   int
	Leased   int
	Free     int
}

// WidthFrom is the pure accounting, so it can be tested without a store.
func WidthFrom(desired, starting, living int) Width {
	leased := starting + living
	return Width{
		Desired:  desired,
		Starting: starting,
		Living:   living,
		Leased:   leased,
		Free:     desired - leased,
	}
}

// GetWidth reads a friend's desired slots and the ZCARDs of its starting and
// living identities, and returns the accounting. It is read only: task width
// without n never writes, and with n it is capacity friend <f> <n> (one
// writer, the capacity function).
func GetWidth(ctx context.Context, st *store.Store, as string) (Width, error) {
	if st == nil {
		return Width{}, fmt.Errorf("task width: nil store")
	}
	if as == "" {
		return Width{}, fmt.Errorf("task width: as is required")
	}
	client := st.Client()
	pipe := client.Pipeline()
	desiredCmd := pipe.HGet(ctx, "friend:"+as+":desired", "slots")
	startingCmd := pipe.ZCard(ctx, "friend:"+as+":starting")
	livingCmd := pipe.ZCard(ctx, "friend:"+as+":living")
	if _, err := pipe.Exec(ctx); err != nil {
		return Width{}, fmt.Errorf("task width: friend %s has no desired slots: %w", as, err)
	}
	desired, err := desiredCmd.Int()
	if err != nil {
		return Width{}, fmt.Errorf("task width: friend %s has no desired slots: %w", as, err)
	}
	return WidthFrom(desired, int(startingCmd.Val()), int(livingCmd.Val())), nil
}
