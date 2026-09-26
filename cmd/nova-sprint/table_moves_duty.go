package main

import (
	"context"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// cardDealDuty (nova-tools #3929, the table moves; card_moves.go has the
// verbs) is the reconciler's deal duty over every consumer (Glenn
// 12:55 PM ET: benches and friends in one ranked pass): each pass returns
// lapsed copies, then for every enrolled consumer with free slots deals
// what it lacks (free - |ready|) and fills its working set, one call each.
type cardDealDuty struct{ st *store.Store }

func (d *cardDealDuty) Run(ctx context.Context, _ *reconcile.Lease) (reconcile.Counts, error) {
	res, err := taskcard.DealPass(ctx, d.st.Client(), "reconciler", time.Now())
	return reconcile.Counts{Dealt: res.Dealt, Expired: res.Expired}, err
}

func init() {
	registerReconcileDuty("card-deal", func(st *store.Store) (reconcileDuty, error) {
		return &cardDealDuty{st: st}, nil
	})
}
