package reconcile_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// boundTTL is the production lease TTL; the write margin is the default 1 s.
const boundTTL = reconcile.DefaultTTL

// slowDevRed is a dev-red duty over n watched bases with a tip and no CI
// record, so every base makes one forge read; forge is that read.
func slowDevRed(t *testing.T, ctx context.Context, c *redis.Client, n int, forge func(ctx context.Context, repo, sha string) (reconcile.CIState, error)) *reconcile.DevRed {
	t.Helper()
	d := &reconcile.DevRed{Client: c, To: "rowan", Forge: forge,
		Push: func(context.Context, reconcile.FixTask) (string, error) {
			return "", errors.New("no push in this test")
		}}
	for i := 0; i < n; i++ {
		repo := fmt.Sprintf("slow-%d", i)
		if err := c.HSet(ctx, civerdict.TipKey(repo, "dev"), "sha", strings.Repeat("e", 40)).Err(); err != nil {
			t.Fatal(err)
		}
		d.Bases = append(d.Bases, land.RepoBase{Repo: repo, Base: "dev"})
	}
	return d
}

// fakeLease is a reconciler lease on the fake clock with no heartbeat, the
// lease a pass sees when its renewals are not landing.
func fakeLease(t *testing.T, ctx context.Context, st *store.Store) (*reconcile.Lease, *fakeClock) {
	t.Helper()
	clk := newFakeClock(time.Unix(1_800_000_000, 0))
	l, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "ctl-host", TTL: boundTTL, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Release(context.Background()) })
	return l, clk
}

// TestSlowDutyPassEndsInsideLease is nova-tools #3805 (DONE-WHEN): a slow
// duty stops starting new work when less than the write margin of the lease
// is left, the duties after it are not started, what was done and what was
// left are recorded in proc:reconciler, and the pass ends inside the lease
// window on the lease clock. The control: the same duty unbounded runs past
// the TTL.
func TestSlowDutyPassEndsInsideLease(t *testing.T) {
	ctx := context.Background()
	const bases, step = 8, 800 * time.Millisecond

	t.Run("control: unbounded, the slow duty overruns the lease", func(t *testing.T) {
		_, c := controlRedis(t)
		clk := newFakeClock(time.Unix(1_800_000_000, 0))
		start := clk.Now()
		calls := 0
		d := slowDevRed(t, ctx, c, bases, func(context.Context, string, string) (reconcile.CIState, error) {
			calls++
			clk.Advance(step)
			return reconcile.CIState{}, nil
		})
		if _, err := d.Pass(ctx); err != nil {
			t.Fatal(err)
		}
		if took := clk.Now().Sub(start); calls != bases || took <= boundTTL {
			t.Fatalf("unbounded: %d forge reads in %s; the fixture must overrun the %s TTL", calls, took, boundTTL)
		}
	})

	t.Run("bounded: stops at the margin, records, ends inside", func(t *testing.T) {
		st, c := controlRedis(t)
		l, clk := fakeLease(t, ctx, st)
		calls, lateRan := 0, false
		d := slowDevRed(t, ctx, c, bases, func(context.Context, string, string) (reconcile.CIState, error) {
			calls++
			clk.Advance(step)
			return reconcile.CIState{}, nil
		})
		late := func(context.Context, *reconcile.Lease) (reconcile.Counts, error) {
			lateRan = true
			return reconcile.Counts{}, nil
		}
		lp := &reconcile.Loop{Lease: l, Duties: []reconcile.Duty{d.Run, late}, Names: []string{"dev-red", "late"}}
		res, err := lp.Pass(ctx)
		if err != nil {
			t.Fatalf("pass: %v", err)
		}
		// Bases start at 0, 0.8 ... 4.8 s (1.2 s left); at 5.6 s 0.4 s is
		// left and dev-red stops. The loop renews before the next duty (the
		// renewal lands here), so the quick late duty still runs.
		if calls != 7 || !lateRan {
			t.Fatalf("forge reads %d (want 7), late duty ran %v (want it run after the renewal)", calls, lateRan)
		}
		if res.Took >= boundTTL || res.Took != 7*step {
			t.Fatalf("pass took %s on the lease clock; want %s, inside the %s lease", res.Took, 7*step, boundTTL)
		}
		for _, want := range []string{"duty 0: dev-red: 1 of 8 base(s) not started: LEASE-MARGIN: 400ms of the lease left, below the 1s write margin"} {
			if !strings.Contains(res.Err, want) {
				t.Fatalf("pass err %q, want it to say %q", res.Err, want)
			}
		}
		if got := procField(t, ctx, c, "err"); got != res.Err {
			t.Fatalf("proc:reconciler err %q, want the pass's %q", got, res.Err)
		}
		if got := procField(t, ctx, c, "took_ms"); got != "5600" {
			t.Fatalf("proc:reconciler took_ms %q, want 5600", got)
		}
		if l.Fenced() {
			t.Fatal("the lease was fenced; the pass record must land inside the lease")
		}
	})

	t.Run("bounded: one slow forge read is cut at the budget", func(t *testing.T) {
		st, c := controlRedis(t)
		l, clk := fakeLease(t, ctx, st)
		lateRan := false
		d := slowDevRed(t, ctx, c, 1, func(ctx context.Context, _, _ string) (reconcile.CIState, error) {
			// A read that would take 10 s on the lease clock.
			for i := 0; i < 100 && ctx.Err() == nil; i++ {
				clk.Advance(100 * time.Millisecond)
				time.Sleep(time.Millisecond)
			}
			if ctx.Err() == nil {
				return reconcile.CIState{Verdict: civerdict.OK}, nil
			}
			return reconcile.CIState{}, ctx.Err()
		})
		late := func(context.Context, *reconcile.Lease) (reconcile.Counts, error) {
			lateRan = true
			return reconcile.Counts{}, nil
		}
		lp := &reconcile.Loop{Lease: l, Duties: []reconcile.Duty{d.Run, late}, Names: []string{"dev-red", "late"}}
		res, err := lp.Pass(ctx)
		if err != nil {
			t.Fatalf("pass: %v", err)
		}
		if res.Took >= boundTTL-500*time.Millisecond || res.Took < boundTTL-reconcile.DefaultWriteMargin {
			t.Fatalf("pass took %s on the lease clock; want the read cut at %s", res.Took, boundTTL-reconcile.DefaultWriteMargin)
		}
		// Cut at the budget, the write margin is left: a quick duty after it
		// still starts, and the pass records inside the lease.
		if !strings.Contains(res.Err, "forge read cut at the lease budget") || !lateRan || l.Fenced() {
			t.Fatalf("pass err %q (late ran %v, fenced %v); want the cut read recorded and the pass held", res.Err, lateRan, l.Fenced())
		}
	})
}

// TestRouteDutyStopsAtLeaseMargin (#3805): with less than the write margin
// of the lease left the route duty makes no move and says what it left;
// with the lease renewed the same pass makes the move.
func TestRouteDutyStopsAtLeaseMargin(t *testing.T) {
	f := newPRFixture(t, "control-3805")
	_ = f.l.Release(f.ctx)
	l, clk := fakeLease(t, f.ctx, store.New(f.c))
	f.addPR(t, "b-3805", "rowan", "3805", headA)

	clk.Advance(boundTTL - 500*time.Millisecond)
	c, err := f.duty.Run(f.ctx, l)
	if !errors.Is(err, reconcile.ErrLeaseMargin) || !strings.Contains(err.Error(), "1 of 1 sprint(s) not started") {
		t.Fatalf("route at 500ms left: %v; want LEASE-MARGIN, 1 of 1 sprint(s) not started", err)
	}
	if c.Routed != 0 || f.qlen("emma")+f.qlen("stella") != 0 {
		t.Fatalf("route at the margin moved %+v; want nothing", c)
	}

	if err := l.Renew(f.ctx); err != nil {
		t.Fatal(err)
	}
	c, err = f.duty.Run(f.ctx, l)
	if err != nil || c.Reads != 1 {
		t.Fatalf("route with the lease renewed: %+v %v; want one read", c, err)
	}
}
