package reconcile

import (
	"context"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Windows are the sweep's expiry clocks (#2756 2.2, 3.2): Start is the launched
// ack window of a dealt reservation, Beat the silence after which a launched or
// running card is reconcile-required. They are policy, not a client clock: the
// age is measured with Redis TIME inside the function.
type Windows struct {
	Start time.Duration
	Beat  time.Duration
}

// DefaultWindows is the spec's interim policy: 60 s start ack, 180 s beat.
var DefaultWindows = Windows{Start: 60 * time.Second, Beat: 180 * time.Second}

// ReclaimRequest expires one card. Fence is the reconciler lease token.
type ReclaimRequest struct {
	Sprint  string
	Label   string
	Fence   string
	Windows Windows
}

// Reclaim is the sweep's expiry for one card (3.2, 5.4):
//
//   - dealt with no launched ack inside Start and no live identity on the
//     bench: QUEUED under a new attempt (reason spawn-timeout), token fenced;
//   - dealt whose identity is live on the bench: LAUNCHED (the ack was lost);
//   - launched or running with no beat inside Beat: REQUIRED
//     (reconcile-required, reason beat-lost), token fenced, slot freed, idem
//     key launch:<S>/<label>/<attempt>. Never requeued from here and the same
//     attempt is never launched again: a lost beat does not prove the card
//     stopped (control 23);
//   - anything else: NOTHING.
func Reclaim(ctx context.Context, st *store.Store, req ReclaimRequest) (Result, error) {
	w := req.Windows
	if w.Start <= 0 || w.Beat <= 0 {
		w = DefaultWindows
	}
	return call(ctx, st, "card reclaim", req.Label, "ns_card_reclaim",
		req.Sprint, req.Label, req.Fence, w.Start.Milliseconds(), w.Beat.Milliseconds())
}
