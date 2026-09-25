// The waiting-resolve duty (nova-tools #3872, #4059): every `nova-sprint
// reconcile` pass moves each waiting task whose DEPENDS-ON have all landed
// and that has a consumer this tick to ready, through registerReconcileDuty,
// so no coordinator walks the waiting sets by hand. The deal duty
// (deal_friend_duty.go) registers right after it, in this one init, so the
// card it releases moves on to working in the same pass: ready is never a
// resting state. The duty is reconcile.WaitingResolve; its RESOLVE lines go
// to the reconciler's stdout.
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
	registerReconcileDuty("deal", buildDealDuty)
}
