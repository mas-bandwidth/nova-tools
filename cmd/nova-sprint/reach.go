package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// storeDown writes the verb's line and answers true when err, from the verb's
// first batch, says the store could not be reached. store.Open sends nothing
// (#3277), so the first batch is the probe, and a verb that exits 6 on an
// unreachable store checks that batch's error here to keep the code.
// storeExit is the connect-redis family's own refusal: the verb's line with
// the shared store classes (storeRefusal) and exit 6.
func storeExit(errOut io.Writer, verb string, err error) int {
	line, _ := storeRefusal(err.Error())
	fmt.Fprintf(errOut, "nova-sprint %s: %s\n", verb, oneline.Escape(line))
	return exitStoreDown
}

// storeCause is the error's text, explained when it is the store's own
// (store.ExplainErr), for a verb whose receipt line carries a why= field.
func storeCause(err error) string {
	if line, ok := store.ExplainErr(err); ok {
		return line
	}
	return err.Error()
}

func storeDown(errOut io.Writer, verb string, err error) bool {
	if err == nil || !(store.Unreachable(err) || strings.Contains(err.Error(), "NOPERM")) {
		return false
	}
	// The same cause and remedy the shared refusal prints (storeRefusal):
	// the store that did not answer, or the ACL user it denied, and the
	// verb that shows it. The caller's exit stays 6.
	line, _ := storeRefusal("connect redis: " + err.Error())
	fmt.Fprintf(errOut, "nova-sprint %s: %s\n", verb, oneline.Escape(line))
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
