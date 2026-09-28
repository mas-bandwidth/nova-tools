//go:build functional

package reconcile_test

import (
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// The route duty's consumers on s:<S>:hold:events (nova-tools #3808): the
// group `route` held 258 consumers because every reconciler instance (each
// restart, each `reconcile --once` probe) read under a new consumer name and
// nothing ever deleted one. One name per instance, created once; a sweep
// deletes consumers idle past ConsumerMaxIdle with nothing pending.

func routeConsumers(t *testing.T, f *prFixture) []redis.XInfoConsumer {
	t.Helper()
	cs, err := f.c.XInfoConsumers(f.ctx, "s:"+f.S+":hold:events", reconcile.RouteGroup).Result()
	if err != nil {
		t.Fatalf("xinfo consumers: %v", err)
	}
	return cs
}

// TestRouteConsumerStableOverHundredPasses: one instance, 100 passes, one
// consumer named for the instance, present from the first pass.
func TestRouteConsumerStableOverHundredPasses(t *testing.T) {
	f := newPRFixture(t, "ctl-3808a")
	for i := 0; i < 100; i++ {
		f.run(t)
		cs := routeConsumers(t, f)
		if len(cs) != 1 || cs[0].Name != "reconciler-"+f.l.Instance() {
			t.Fatalf("pass %d: consumers %+v; want exactly reconciler-%s", i, cs, f.l.Instance())
		}
	}
}

// TestRouteConsumerSweepBoundsRestarts: 100 restarts (a new lease instance
// each, as `reconcile --once` probes are) keep the group at most two
// consumers, the live one and the one just left; a dead consumer's pending
// hold event is claimed and routed before its consumer is deleted.
func TestRouteConsumerSweepBoundsRestarts(t *testing.T) {
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")
	f := newPRFixture(t, "ctl-3808b")
	ev := "s:" + f.S + ":hold:events"
	// A dead instance read one note entry and never acknowledged it.
	if err := f.c.XGroupCreateMkStream(f.ctx, ev, reconcile.RouteGroup, "0").Err(); err != nil {
		t.Fatal(err)
	}
	f.c.XAdd(f.ctx, &redis.XAddArgs{Stream: ev, Values: []any{"type", "note", "unit", "u1"}})
	if err := f.c.XReadGroup(f.ctx, &redis.XReadGroupArgs{Group: reconcile.RouteGroup, Consumer: "reconciler-dead",
		Streams: []string{ev, ">"}, Count: 10, Block: -1}).Err(); err != nil {
		t.Fatal(err)
	}
	st := store.New(f.c)
	for i := 0; i < 100; i++ {
		if err := f.l.Release(f.ctx); err != nil {
			t.Fatalf("restart %d: release: %v", i, err)
		}
		l, err := reconcile.Acquire(f.ctx, st, reconcile.AcquireOptions{Host: "ctl-host"})
		if err != nil {
			t.Fatalf("restart %d: acquire: %v", i, err)
		}
		f.l = l
		f.duty.ConsumerMaxIdle = time.Millisecond
		time.Sleep(2 * time.Millisecond)
		f.run(t)
		cs := routeConsumers(t, f)
		if len(cs) > 2 {
			t.Fatalf("restart %d: %d consumers %+v; want at most 2", i, len(cs), cs)
		}
		for _, c := range cs {
			if c.Name == "reconciler-dead" && i > 0 {
				t.Fatalf("restart %d: dead consumer survived the sweep: %+v", i, cs)
			}
		}
	}
	pend, err := f.c.XPending(f.ctx, ev, reconcile.RouteGroup).Result()
	if err != nil {
		t.Fatal(err)
	}
	if pend.Count != 0 {
		t.Fatalf("pending %d; want 0 (the dead consumer's entry claimed and acked)", pend.Count)
	}
}

// TestRouteConsumerSweepKeepsRecent: the default hour keeps a
// fresh consumer from another instance.
func TestRouteConsumerSweepKeepsRecent(t *testing.T) {
	f := newPRFixture(t, "ctl-3808c")
	ev := "s:" + f.S + ":hold:events"
	if err := f.c.XGroupCreateMkStream(f.ctx, ev, reconcile.RouteGroup, "0").Err(); err != nil {
		t.Fatal(err)
	}
	f.c.XGroupCreateConsumer(f.ctx, ev, reconcile.RouteGroup, "reconciler-other")
	f.run(t)
	if cs := routeConsumers(t, f); len(cs) != 2 {
		t.Fatalf("consumers %+v; want the fresh other plus ours (default idle is an hour)", cs)
	}
}
