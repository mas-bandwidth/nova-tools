package task

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// Width is the slot accounting of one friend (spec 2.2, 4.2). Leased is
// ZCARD friend:<f>:cards:working, the one lease ledger (#3998): taken
// tasks, task cards and copies, global to the consumer, so it already sums
// over every open sprint; Free is desired minus leased.
type Width struct {
	Desired int
	Leased  int
	Free    int
}

// WidthFrom is the pure accounting, so it can be tested without a store.
func WidthFrom(desired, leased int) Width {
	return Width{Desired: desired, Leased: leased, Free: desired - leased}
}

// GetWidth reads a friend's desired slots and the ZCARD of its working set,
// and returns the accounting. It is read only: task width
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
	pipe := client.Pipeline()
	desiredCmd := pipe.HGet(ctx, "friend:"+as+":desired", "slots")
	// the working set under the current epoch, counted in this one
	// pipeline (nova-tools#4238)
	workingCmd := ws.CellCard(ctx, pipe, "friend:"+as, "working")
	if _, err := pipe.Exec(ctx); err != nil {
		return Width{}, fmt.Errorf("width: friend %s has no desired slots: %w", as, err)
	}

	desired, err := desiredCmd.Int()
	if err != nil {
		return Width{}, fmt.Errorf("width: friend %s has no desired slots: %w", as, err)
	}
	working, err := workingCmd.Int64()
	if err != nil {
		return Width{}, fmt.Errorf("width: friend %s working: %w", as, err)
	}
	return WidthFrom(desired, int(working)), nil

}
