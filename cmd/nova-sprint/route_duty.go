// The route duty of `nova-sprint reconcile` (nova-tools #3323, #3199): the
// reconciler serves every open sprint's routes in its pass, where
// `nova-sprint route` serves one sprint by flag. It registers through
// registerReconcileDuty, so the fleet's one reconcile unit runs it with no
// unit of its own. The moves are reconcile.RouteDuty's: an OK card with a PR
// record and a JEV line to one read task on the least-loaded reading friend,
// a HOLD at head to one fix task on the author's queue, a scored green PR to
// merging. --readers a,b names the reading friends; empty, the `readers` SET,
// else every friend with a live beat.
package main

import (
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// reconcileReaders is --readers, set before the duties are built.
var reconcileReaders []string

func init() {
	registerReconcileDuty("route", func(st *store.Store) (reconcileDuty, error) {
		return &reconcile.RouteDuty{Client: st.Client(), Readers: reconcileReaders}, nil
	})
}
