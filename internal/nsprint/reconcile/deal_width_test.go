package reconcile_test

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
)

// TestDealSessionsGetTheFullLeaseWindow (#3706, sprint quack-0925b
// 2026-09-25 03:59Z): the duties before the deal spent the lease, so the
// benches' sessions found `no session opened: 1.726s of lease left after the
// 1s write margin` and read WEDGED, and their cards bounced to queued. Here a
// duty spends 5.2 s of the 6 s lease on the lease clock before the refill
// runs, leaving less than the write margin. The deal pass renews the lease
// before it opens the sessions, so every one of the six benches' sessions is
// bounded by the full TTL less the margin, every bench is dealt and its row
// reads ok, and the lease never lapses.
func TestDealSessionsGetTheFullLeaseWindow(t *testing.T) {
	t.Parallel()

	st, c := controlRedis(t)
	ctx := context.Background()
	const S = "control-00003706"
	benches := []string{"ctl-batman", "ctl-hetzner", "ctl-hulk", "ctl-space", "ctl-superman", "ctl-vision"}
	for _, b := range benches {
		seedBench(t, c, b, 2)
	}
	seedSprint(t, c, S, 1, 2*len(benches))

	clk := newFakeClock(time.Unix(1_800_000_000, 0))
	l, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "ctl-host", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	lapsed := leaseOnClock(t, c, clk, l)
	const spent = 5200 * time.Millisecond
	spend := func(context.Context, *reconcile.Lease) (reconcile.Counts, error) {
		clk.Advance(spent)
		return reconcile.Counts{}, nil
	}
	dialer := &fakeDialer{}
	rf := &reconcile.Refill{Client: c, Deal: &deal.Pass{Dialer: dialer}, Now: clk.Now}
	lp := &reconcile.Loop{Lease: l, Duties: []reconcile.Duty{spend, rf.Run}}

	p := mustPass(t, lp)
	if lapsed() {
		t.Fatal("the lease lapsed on the lease clock during the pass")
	}
	bound := reconcile.DefaultTTL - reconcile.DefaultWriteMargin
	bounds := clk.bounds()
	if len(bounds) != len(benches) {
		t.Fatalf("%d sessions bounded by the lease, want %d (one per bench): %v", len(bounds), len(benches), bounds)
	}
	for _, b := range bounds {
		if b != bound {
			t.Fatalf("session bound %s, want the full lease TTL %s less the write margin = %s: the lease was not renewed before the sessions (%s already spent)", b, reconcile.DefaultTTL, bound, spent)
		}
	}
	if p.Counts.Dealt != 2*len(benches) {
		t.Fatalf("dealt %d, want %d", p.Counts.Dealt, 2*len(benches))
	}
	for _, b := range benches {
		row, err := c.HGetAll(ctx, deal.RowKey(b)).Result()
		if err != nil {
			t.Fatal(err)
		}
		if row["state"] != deal.SSHOK {
			t.Errorf("%s row %v, want ok", b, row)
		}
		if sessions, lines := dialer.count(b); sessions != 1 || lines != 2 {
			t.Errorf("%s sessions %d lines %d, want 1 and 2", b, sessions, lines)
		}
		if got := leased(t, c, b); got != 2 {
			t.Errorf("%s leased %d, want 2", b, got)
		}
	}
	if got := openCards(t, c, S); got != 0 {
		t.Fatalf("open %d, want 0: every card dealt in the one pass", got)
	}
}
