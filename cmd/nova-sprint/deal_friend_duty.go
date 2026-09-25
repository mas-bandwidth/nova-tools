// The friend deal duty of `nova-sprint reconcile` (nova-tools #3873): every
// pass fills every live friend's open slots from ws:<stream>:ready in ws:order
// rank order, honouring WHO and kind, one ns_deal_friend call per friend, and
// moves a card no live consumer may take back to waiting (why=no-consumer).
// It registers through registerReconcileDuty, so the fleet's one reconcile
// unit runs it with no unit of its own; its DEAL receipt lines go to the
// verb's stdout.
package main

import (
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// reconcileOut is the verb's stdout, set before the duties are built.
var reconcileOut io.Writer

func init() {
	registerReconcileDuty("deal", func(st *store.Store) (reconcileDuty, error) {
		return &reconcile.FriendDeal{Client: st.Client(), Out: reconcileOut}, nil
	})
}
