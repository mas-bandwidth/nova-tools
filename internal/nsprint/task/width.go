package task

import (
	"context"
	"fmt"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Width is the slot accounting of one friend (spec 2.2, 4.2). Leased is
// ZCARD friend:<f>:cards:working, the one lease ledger (#3998): taken
// tasks, task cards and copies, global to the consumer, so it already sums
// over every open sprint; CI is the CI legs running on the friend's
// machine, its beat's ci (nova-tools#4293), each holding a slot; Free is
// desired minus CI minus leased.
type Width struct {
	Desired int
	Leased  int
	CI      int
	Free    int
}

// WidthFrom is the pure accounting, so it can be tested without a store.
func WidthFrom(desired, leased, ci int) Width {
	return Width{Desired: desired, Leased: leased, CI: ci, Free: desired - ci - leased}
}

// GetWidth reads a friend's desired slots and the ZCARD of its working set,
// and returns the accounting. It is read only: task width
// without n never writes, and with n it is capacity friend --as <f> --slots <n> (one
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
	workingCmd := pipe.ZCard(ctx, "friend:"+as+":cards:working")
	ciCmd := pipe.HMGet(ctx, "friend:"+as+":beat", "ci") // HMGet: an absent field is nil, never redis.Nil
	if _, err := pipe.Exec(ctx); err != nil {
		return Width{}, fmt.Errorf("width: friend %s has no desired slots: %w", as, err)
	}
	desired, err := desiredCmd.Int()
	if err != nil {
		return Width{}, fmt.Errorf("width: friend %s has no desired slots: %w", as, err)
	}
	ci := 0 // absent or unmeasured reads as none
	if v := ciCmd.Val(); len(v) == 1 {
		if s, ok := v[0].(string); ok {
			ci, _ = strconv.Atoi(s)
		}
	}
	return WidthFrom(desired, int(workingCmd.Val()), ci), nil
}
