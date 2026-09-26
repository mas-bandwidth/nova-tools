package reconcile_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// heartbeatTTL is a short lease TTL on the wall clock: the heartbeat is TTL/3.
const heartbeatTTL = 600 * time.Millisecond

// fencedWrite is a duty's own fenced write, the way every duty makes one: a
// nova_sprint function called with the lease token that refuses FENCED
// unless the token holds lease:reconciler.
func fencedWrite(ctx context.Context, c *redis.Client, l *reconcile.Lease) error {
	reply, err := c.FCall(ctx, "ns_reconciler_renew", nil, l.Token(), l.TTL().Milliseconds()).StringSlice()
	if err != nil {
		return err
	}
	if len(reply) == 0 || reply[0] != "OK" {
		return reconcile.ErrFenced
	}
	return nil
}

func leaseHolder(t *testing.T, c *redis.Client) (instance, token string) {
	t.Helper()
	h, err := c.HMGet(context.Background(), reconcile.LeaseKey, "instance", "token").Result()
	if err != nil {
		t.Fatal(err)
	}
	instance, _ = h[0].(string)
	token, _ = h[1].(string)
	return instance, token
}

// TestHeartbeatHoldsLeaseThroughLongDuty is nova-tools #3737 (DONE-WHEN): a
// duty that runs three lease TTLs keeps the lease, same instance and same
// token, and its fenced writes and the pass record succeed, because the
// lease renews itself every TTL/3 whatever the duty is doing. The control:
// the same duty under a lease with no heartbeat is FENCED, the Studio's
// restart loop of 2026-09-24/25.
func TestHeartbeatHoldsLeaseThroughLongDuty(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	st, c := controlRedis(t)
	ctx := context.Background()
	longDuty := func(ctx context.Context, l *reconcile.Lease) (reconcile.Counts, error) {
		time.Sleep(3 * heartbeatTTL)
		if err := fencedWrite(ctx, c, l); err != nil {
			return reconcile.Counts{}, err
		}
		return reconcile.Counts{Dealt: 1}, nil
	}

	t.Run("no heartbeat: fenced (the defect)", func(t *testing.T) {
		l, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "ctl-host", TTL: heartbeatTTL})
		if err != nil {
			t.Fatal(err)
		}
		if l.Heartbeat() != 0 {
			t.Fatalf("heartbeat %s without AcquireOptions.Heartbeat, want 0", l.Heartbeat())
		}
		lp := &reconcile.Loop{Lease: l, Interval: 100 * time.Millisecond, Duties: []reconcile.Duty{longDuty}}
		if _, err := lp.Pass(ctx); !errors.Is(err, reconcile.ErrFenced) {
			t.Fatalf("pass over a %s duty with no heartbeat = %v, want ErrFenced", 3*heartbeatTTL, err)
		}
		if !l.Fenced() || !l.Deadline().IsZero() || l.Remaining() != 0 {
			t.Fatalf("after the fence: fenced %v deadline %v remaining %s; want fenced, no deadline, 0", l.Fenced(), l.Deadline(), l.Remaining())
		}
		c.Del(ctx, reconcile.LeaseKey)
	})

	t.Run("heartbeat: held, same instance and token", func(t *testing.T) {
		l, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "ctl-host", TTL: heartbeatTTL, Heartbeat: true})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Release(ctx) }()
		if l.Heartbeat() != heartbeatTTL/3 || reconcile.HeartbeatEvery(reconcile.DefaultTTL) != 2*time.Second {
			t.Fatalf("heartbeat %s (want %s), at the default TTL %s (want 2s)", l.Heartbeat(), heartbeatTTL/3, reconcile.HeartbeatEvery(reconcile.DefaultTTL))
		}
		inst, tok := leaseHolder(t, c)
		lp := &reconcile.Loop{Lease: l, Interval: 100 * time.Millisecond, Duties: []reconcile.Duty{longDuty}}
		res, err := lp.Pass(ctx)
		if err != nil {
			t.Fatalf("pass over a %s duty under a heartbeat: %v", 3*heartbeatTTL, err)
		}
		if res.Counts.Dealt != 1 || res.Took < 3*heartbeatTTL {
			t.Fatalf("pass dealt %d took %s; want the duty's write and at least %s", res.Counts.Dealt, res.Took, 3*heartbeatTTL)
		}
		if i2, t2 := leaseHolder(t, c); i2 != inst || t2 != tok || i2 != l.Instance() || t2 != l.Token() {
			t.Fatal("the lease changed hands or token during the duty; want the same instance and token")
		}
		if l.Fenced() {
			t.Fatal("lease fenced after a held pass")
		}
		if got := procField(t, ctx, c, "instance"); got != l.Instance() {
			t.Fatalf("proc:reconciler instance %q, want %q", got, l.Instance())
		}
	})
}

// TestHeartbeatFencesWithinOneBeat: with the lease key deleted under a
// heartbeating lease, the next beat (one heartbeat on the lease clock) is
// refused FENCED and fences the lease: Done closes, every write through the
// lease refuses without a round trip, Deadline is the zero time, a deal
// fence refuses its token, and the pass loop exits ErrFenced.
func TestHeartbeatFencesWithinOneBeat(t *testing.T) {
	t.Parallel()

	st, c := controlRedis(t)
	ctx := context.Background()
	clk := newFakeClock(time.Unix(1_800_000_000, 0))
	l, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "ctl-host", Clock: clk, Heartbeat: true})
	if err != nil {
		t.Fatal(err)
	}
	defer l.StopHeartbeat()
	every := l.Heartbeat()
	waitArmed(t, clk)

	// One beat renews: the deadline moves with the lease clock.
	clk.Advance(every)
	waitArmed(t, clk)
	if want := clk.Now().Add(l.TTL()); !l.Deadline().Equal(want) {
		t.Fatalf("deadline %v after one beat, want %v", l.Deadline(), want)
	}

	c.Del(ctx, reconcile.LeaseKey)
	if l.Fenced() {
		t.Fatal("fenced before the next beat")
	}
	clk.Advance(every)
	select {
	case <-l.Done():
	case <-time.After(eventWait()):
		t.Fatalf("no fence within one heartbeat (%s on the lease clock) of the lease key's deletion", every)
	}
	if !l.Fenced() || !l.Deadline().IsZero() || l.Remaining() != 0 {
		t.Fatalf("fenced %v deadline %v remaining %s; want fenced, zero, 0", l.Fenced(), l.Deadline(), l.Remaining())
	}
	before := fcalls(t, c)
	if err := l.Renew(ctx); !errors.Is(err, reconcile.ErrFenced) {
		t.Fatalf("renew after the fence = %v, want ErrFenced", err)
	}
	if n := fcalls(t, c) - before; n != 0 {
		t.Fatalf("a fenced lease sent %d FCALLs, want 0", n)
	}
	if _, err := (reconcile.LeaseFence{L: l}).Token(ctx); !errors.Is(err, reconcile.ErrFenced) {
		t.Fatalf("deal fence token after the fence = %v, want ErrFenced", err)
	}
	lp := &reconcile.Loop{Lease: l, Interval: 100 * time.Millisecond}
	if err := lp.Run(ctx); !errors.Is(err, reconcile.ErrFenced) {
		t.Fatalf("loop after the fence = %v, want ErrFenced", err)
	}
	if l.Token() == "" {
		t.Fatal("the token changed; it never does")
	}
}

// TestHeartbeatFencesWhenRedisIsGone: renewals that fail on the wire are
// retried every beat; once a whole TTL passes with none succeeding the Redis
// lease has lapsed, so the lease fences itself.
func TestHeartbeatFencesWhenRedisIsGone(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	own := redis.NewClient(&redis.Options{Addr: addr})
	l, err := reconcile.Acquire(ctx, store.New(own), reconcile.AcquireOptions{Host: "ctl-host", TTL: heartbeatTTL, Heartbeat: true})
	if err != nil {
		t.Fatal(err)
	}
	defer l.StopHeartbeat()
	_ = own.Close() // this instance's Redis is gone
	select {
	case <-l.Done():
	case <-time.After(eventWait()):
		t.Fatal("no fence after the lease TTL with Redis unreachable")
	}
	if err := l.Renew(ctx); !errors.Is(err, reconcile.ErrFenced) || !strings.Contains(err.Error(), "no renewal for the lease TTL") {
		t.Fatalf("renew = %v, want ErrFenced naming the TTL with no renewal", err)
	}
}

// TestRenewCoalesces: the heartbeat, the pass start and every deal worker
// share one renewal mechanism (#3737, #3706's window): renewals asked inside
// RenewAfter of the last successful one send nothing, and concurrent ones
// share the one on the wire.
func TestRenewCoalesces(t *testing.T) {
	t.Parallel()

	st, c := controlRedis(t)
	ctx := context.Background()
	clk := newFakeClock(time.Unix(1_800_000_000, 0))
	l, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "ctl-host", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	renewAll := func() {
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := l.Renew(ctx); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
	}
	before := fcalls(t, c)
	renewAll() // inside the window of the acquire
	if n := fcalls(t, c) - before; n != 0 {
		t.Fatalf("8 renewals inside the window of the acquire sent %d FCALLs, want 0", n)
	}
	clk.Advance(reconcile.DefaultRenewAfter)
	before = fcalls(t, c)
	renewAll()
	if n := fcalls(t, c) - before; n != 1 {
		t.Fatalf("8 concurrent renewals past the window sent %d FCALLs, want 1", n)
	}
}

// waitArmed waits for the heartbeat to arm its next timer on the clock.
func waitArmed(t *testing.T, clk *fakeClock) {
	t.Helper()
	select {
	case <-clk.armed:
	case <-time.After(eventWait()):
		t.Fatal("the heartbeat never armed its timer")
	}
}

// fcalls is the server's FCALL count (INFO commandstats).
func fcalls(t *testing.T, c *redis.Client) int {
	t.Helper()
	info, err := c.Info(context.Background(), "commandstats").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(info, "\n") {
		if !strings.HasPrefix(line, "cmdstat_fcall:") {
			continue
		}
		for _, kv := range strings.Split(strings.TrimPrefix(strings.TrimSpace(line), "cmdstat_fcall:"), ",") {
			if v, ok := strings.CutPrefix(kv, "calls="); ok {
				n, _ := strconv.Atoi(v)
				return n
			}
		}
	}
	return 0
}
