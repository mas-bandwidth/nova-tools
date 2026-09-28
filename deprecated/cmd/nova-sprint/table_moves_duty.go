package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
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
//
// Every line the pass produced (EXPIRED, READS, DEAL, and the REFUSED and
// SKIP lines of a consumer it could not deal to) goes to the verb's stdout,
// and the pass's refusals are its error, so the DUTY line carries them
// (Glenn 2026-09-26: no verb on the live path fails silently; before this
// a Deal refusal, a Redis error and a consumer with an unreadable slots
// field all vanished with the lines).
type cardDealDuty struct {
	st  *store.Store
	out io.Writer
}

func (d *cardDealDuty) Run(ctx context.Context, _ *reconcile.Lease) (reconcile.Counts, error) {
	res, err := taskcard.DealPass(ctx, d.st.Client(), "reconciler", time.Now())
	out := d.out
	if out == nil {
		out = os.Stdout
	}
	var refused []string
	for _, l := range res.Lines {
		fmt.Fprintln(out, "DEAL-DUTY "+l)
		if strings.Contains(l, " REFUSED ") || strings.Contains(l, " SKIP ") {
			refused = append(refused, l)
		}
	}
	if err == nil && len(refused) > 0 {
		err = fmt.Errorf("deal pass: %d refused: %s", len(refused), strings.Join(refused, "; "))
	}
	return reconcile.Counts{Dealt: res.Dealt, Expired: res.Expired}, err
}

func init() {
	registerReconcileDuty("card-deal", func(st *store.Store) (reconcileDuty, error) {
		return &cardDealDuty{st: st, out: reconcileOut}, nil
	})
}
