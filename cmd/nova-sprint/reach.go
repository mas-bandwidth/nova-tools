package main

import (
	"context"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// storeDown writes the verb's line and answers true when err, from the verb's
// first batch, says the store could not be reached. store.Open sends nothing
// (#3277), so the first batch is the probe, and a verb that exits 6 on an
// unreachable store checks that batch's error here to keep the code.
func storeDown(errOut io.Writer, verb string, err error) bool {
	if !store.Unreachable(err) {
		return false
	}
	fmt.Fprintf(errOut, "nova-sprint %s: connect redis: %v\n", verb, err)
	return true
}

// openReached is store.Open plus one PING, for a long-lived verb (a loop or a
// daemon) that must refuse at start with exit 6 rather than loop on a store it
// cannot reach. One PING per process start is not the per-invocation round
// trip #3277 removed; a one-shot verb uses store.Open and storeDown.
func openReached(ctx context.Context, addr string) (*store.Store, error) {
	st, err := store.Open(ctx, addr)
	if err != nil {
		return nil, err
	}
	if err := st.Reach(ctx); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("redis at %s: %w", addr, err)
	}
	return st, nil
}
