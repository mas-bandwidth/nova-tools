// The deal duty of `nova-sprint reconcile` (nova-tools #3873, #4059): every
// pass moves each ws:<stream>:ready card to working the same tick, to a live
// friend's open seat (honouring WHO and kind, one ns_deal_friend call per
// friend) or, by its route, to the swarm (one ns_deal_swarm call). It never
// moves a card back to waiting. It registers through registerReconcileDuty
// right after the waiting-resolve duty (waiting_resolve.go), so a card the
// resolve releases is dealt in the same pass; its DEAL receipt lines go to
// the verb's stdout.
package main

import (
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// reconcileOut is the verb's stdout, set before the duties are built.
var reconcileOut io.Writer

// buildDealDuty builds the deal duty over st.
func buildDealDuty(st *store.Store) (reconcileDuty, error) {
	return &reconcile.FriendDeal{Client: st.Client(), Out: reconcileOut}, nil
}
