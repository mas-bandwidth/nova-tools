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
		return Width{}, fmt.Errorf("width: nil store")
	}
	if as == "" {
		return Width{}, fmt.Errorf("width: as is required")
	}
	client := st.Client()
	desired, err := client.HGet(ctx, "friend:"+as+":desired", "slots").Int()
	if err != nil {
		return Width{}, fmt.Errorf("width: friend %s has no desired slots: %w", as, err)
	}
	starting, err := client.ZCard(ctx, "friend:"+as+":starting").Result()
	if err != nil {
		return Width{}, fmt.Errorf("width: friend %s starting: %w", as, err)
	}
	living, err := client.ZCard(ctx, "friend:"+as+":living").Result()
	if err != nil {
		return Width{}, fmt.Errorf("width: friend %s living: %w", as, err)
	}
	return WidthFrom(desired, int(starting), int(living)), nil
}
