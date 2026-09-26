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

// slowDevRed is a dev-red duty over n watched bases, each with a tip whose
// CI record is red, so every base makes one fix-task push; push is that
// push, the duty's one step outside its own Redis reads (#3597 left it no
// forge read).
func slowDevRed(t *testing.T, ctx context.Context, c *redis.Client, n int, push func(ctx context.Context, t reconcile.FixTask) (string, error)) *reconcile.DevRed {
	t.Helper()
	d := &reconcile.DevRed{Client: c, To: "rowan", Push: push}
	sha := strings.Repeat("e", 40)
	for i := 0; i < n; i++ {
		repo := fmt.Sprintf("slow-%d", i)
		if err := c.HSet(ctx, civerdict.TipKey(repo, "dev"), "sha", sha).Err(); err != nil {
			t.Fatal(err)
		}
		if err := c.HSet(ctx, reconcile.CIRecordKey(repo, sha), civerdict.Field, "FAIL", "check", "TestSlow").Err(); err != nil {
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
	t.Parallel()

	ctx := context.Background()
	const bases, step = 8, 800 * time.Millisecond

	t.Run("control: unbounded, the slow duty overruns the lease", func(t *testing.T) {
		_, c := controlRedis(t)
		clk := newFakeClock(time.Unix(1_800_000_000, 0))
		start := clk.Now()
		calls := 0
		d := slowDevRed(t, ctx, c, bases, func(_ context.Context, ft reconcile.FixTask) (string, error) {
			calls++
			clk.Advance(step)
			return ft.ID, nil
		})
		if _, err := d.Pass(ctx); err != nil {
			t.Fatal(err)
		}
		if took := clk.Now().Sub(start); calls != bases || took <= boundTTL {
			t.Fatalf("unbounded: %d pushes in %s; the fixture must overrun the %s TTL", calls, took, boundTTL)
		}
	})

	t.Run("bounded: stops at the margin, records, ends inside", func(t *testing.T) {
		st, c := controlRedis(t)
		l, clk := fakeLease(t, ctx, st)
		calls, lateRan := 0, false
		d := slowDevRed(t, ctx, c, bases, func(_ context.Context, ft reconcile.FixTask) (string, error) {
			calls++
			clk.Advance(step)
			return ft.ID, nil
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
			t.Fatalf("pushes %d (want 7), late duty ran %v (want it run after the renewal)", calls, lateRan)
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

// TestPreDutyRenewalFailureSkipsLaterDuties is nova-tools #3838 (DONE-WHEN):
// a renewal seam that fails (non-FENCED, e.g. Redis slow or unreachable) when
// the pre-duty renewal is attempted shows the later duties not started,
// proc:reconciler err naming them (LEASE-MARGIN), and the pass took under the
// TTL on the lease clock while the pass record lands.
func TestPreDutyRenewalFailureSkipsLaterDuties(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const bases, step = 8, 800 * time.Millisecond

	st, c := controlRedis(t)
	l, clk := fakeLease(t, ctx, st)
	calls, late1Ran, late2Ran := 0, false, false
	d := slowDevRed(t, ctx, c, bases, func(_ context.Context, ft reconcile.FixTask) (string, error) {
		calls++
		clk.Advance(step)
		return ft.ID, nil
	})
	late1 := func(context.Context, *reconcile.Lease) (reconcile.Counts, error) {
		late1Ran = true
		return reconcile.Counts{}, nil
	}
	late2 := func(context.Context, *reconcile.Lease) (reconcile.Counts, error) {
		late2Ran = true
		return reconcile.Counts{}, nil
	}

	// Renewal seam fails with a non-FENCED error (e.g. Redis slow or unreachable).
	renewalAttempts := 0
	l.RenewSeam = func(context.Context) error {
		renewalAttempts++
		return errors.New("connection timed out: redis unreachable")
	}

	lp := &reconcile.Loop{
		Lease:  l,
		Duties: []reconcile.Duty{d.Run, late1, late2},
		Names:  []string{"dev-red", "late1", "late2"},
	}
	res, err := lp.Pass(ctx)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}

	// dev-red stopped at 5.6s (0.4s left, below the 1s write margin).
	// Before late1, pre-duty renewal was attempted and failed via the seam.
	// Both late1 and late2 must not start.
	if calls != 7 {
		t.Fatalf("pushes %d (want 7)", calls)
	}
	if late1Ran || late2Ran {
		t.Fatalf("late duties ran (late1=%v, late2=%v); want both skipped", late1Ran, late2Ran)
	}
	if renewalAttempts != 1 {
		t.Fatalf("renewal attempts = %d; want 1 pre-duty renewal attempt", renewalAttempts)
	}
	if res.Took >= boundTTL || res.Took != 7*step {
		t.Fatalf("pass took %s on the lease clock; want %s, inside the %s lease", res.Took, 7*step, boundTTL)
	}

	wantErr := "duties not started late1,late2: LEASE-MARGIN: 400ms of the lease left, below the 1s write margin"
	if !strings.Contains(res.Err, wantErr) {
		t.Fatalf("pass err %q, want it to contain %q", res.Err, wantErr)
	}
	wantDevRedErr := "duty 0: dev-red: 1 of 8 base(s) not started: LEASE-MARGIN: 400ms of the lease left, below the 1s write margin"
	if !strings.Contains(res.Err, wantDevRedErr) {
		t.Fatalf("pass err %q, want it to contain %q", res.Err, wantDevRedErr)
	}

	// Pass record must land in proc:reconciler inside the lease.
	if got := procField(t, ctx, c, "err"); got != res.Err {
		t.Fatalf("proc:reconciler err %q, want the pass's %q", got, res.Err)
	}
	if got := procField(t, ctx, c, "took_ms"); got != "5600" {
		t.Fatalf("proc:reconciler took_ms %q, want 5600", got)
	}
	if l.Fenced() {
		t.Fatal("the lease was fenced; the pass record must land inside the lease")
	}
}
