// The waiting-resolve duty (nova-tools #3872): every `nova-sprint reconcile`
// pass moves each waiting task whose DEPENDS-ON have all landed to ready,
// through registerReconcileDuty, so no coordinator walks the waiting sets by
// hand. The duty is reconcile.WaitingResolve; its RESOLVE lines go to the
// reconciler's stdout.
package main

import (
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// waitingResolveOut receives the duty's RESOLVE receipt lines.
var waitingResolveOut io.Writer = os.Stdout

func init() {
	registerReconcileDuty("waiting-resolve", func(st *store.Store) (reconcileDuty, error) {
		return &reconcile.WaitingResolve{Client: st.Client(), Out: waitingResolveOut}, nil
	})
}
