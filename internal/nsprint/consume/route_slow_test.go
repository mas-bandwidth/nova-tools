//go:build slow

package consume

// TestRouteIsOneProcess runs two real routers against a private redis-server
// and waits, on the wall clock, for the harvest task, the report read and
// every rule's passes (a 60 s bound it waited out under load: space shard
// 3/4 of run 36206780711). A functional test by Glenn's rule (2026-09-25:
// "Anything that waits on real wall clock, timeouts all that -- that's a
// functional test"), behind the slow tag: run by hand (go test -tags slow
// ./internal/nsprint/consume -run TestRouteIsOneProcess) and nightly. The
// per-commit unit version of the lease property is the next card.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// TestRouteIsOneProcess is #3036 (#2756 section 11 row 2): the ok-to-friend,
// report, pr-to-read and hold-to-fix rules run inside one `nova-sprint route`
// process under one lease; a second instance is refused and runs nothing; a
// lease taken away stops the holder; a clean stop releases the lease so the
// next instance starts.
func TestRouteIsOneProcess(t *testing.T) {
	t.Parallel()

	st, client := controlRedis(t)
	sprint := "control-3036c0d2"
	seedSprint(t, client, sprint)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	prA, holdA := &rtFake{}, &rtFake{}
	a := rtRouter(st, sprint, "route-a", prA, holdA)
	doneA := make(chan error, 1)
	go func() { doneA <- a.Run(ctx) }()
	rtUntil(t, "route-a holding the lease", func() bool {
		v, _ := client.HGet(context.Background(), LeaseKey(sprint), "instance").Result()
		return v == "route-a"
	})

	// A second instance is refused before it starts any rule.
	prB, holdB := &rtFake{}, &rtFake{}
	b := rtRouter(st, sprint, "route-b", prB, holdB)
	err := b.Run(context.Background())
	var held *LeaseHeldError
	if !errors.Is(err, ErrLeaseHeld) || !errors.As(err, &held) || held.Holder != "route-a" {
		t.Fatalf("second instance Run = %v; want refused, lease held by route-a", err)
	}
	if prB.started.Load()+prB.passes.Load()+holdB.started.Load()+holdB.passes.Load() != 0 {
		t.Fatal("the refused instance ran a rule")
	}
	if v, _ := client.HGet(context.Background(), LeaseKey(sprint), "instance").Result(); v != "route-a" {
		t.Fatalf("the refused instance changed the lease holder to %q", v)
	}

	// Every rule runs in route-a's process: the real rules read their groups
	// as consumer route-a, and the #2941 fakes are passed by route-a.
	endCard(t, client, sprint, "card-p", "DONE", "done", "internal/x/p.go", "ctl-a")
	endNoCommit(t, client, sprint, "card-q", "ctl-b")
	rtUntil(t, "the harvest task, the report read and passes of every rule", func() bool {
		tasks := rtTaskIDs(t, client, sprint)
		return tasks["harvest-card-p"] != nil && tasks[ReportReadID("card-q", "1")] != nil &&
			prA.passes.Load() > 1 && holdA.passes.Load() > 1
	})
	if prA.started.Load() != 1 || holdA.started.Load() != 1 {
		t.Fatalf("pr-to-read started %d times, hold-to-fix %d; want once each", prA.started.Load(), holdA.started.Load())
	}
	for _, g := range []string{GroupOkFriend, GroupReport} {
		consumers, err := client.XInfoConsumers(context.Background(), "s:"+sprint+":log", g).Result()
		must(t, err)
		if len(consumers) != 1 || consumers[0].Name != "route-a" {
			t.Fatalf("group %s consumers %v; want only route-a", g, consumers)
		}
	}
	lease, err := client.HGetAll(context.Background(), LeaseKey(sprint)).Result()
	must(t, err)
	if lease["token"] == "" || lease["host"] != "ctl-host" || lease["at"] == "" {
		t.Fatalf("lease %v; want instance, token, host and at", lease)
	}
	if ttl, _ := client.PTTL(context.Background(), LeaseKey(sprint)).Result(); ttl <= 0 {
		t.Fatalf("lease PTTL %v; want a live TTL", ttl)
	}

	// A clean stop releases the lease; the next instance takes it and runs.
	cancel()
	if err := <-doneA; err != nil {
		t.Fatalf("route-a Run = %v; want nil on a clean stop", err)
	}
	if n, _ := client.Exists(context.Background(), LeaseKey(sprint)).Result(); n != 0 {
		t.Fatal("a clean stop left the lease behind")
	}
	ctxC, cancelC := context.WithCancel(context.Background())
	defer cancelC()
	prC, holdC := &rtFake{}, &rtFake{}
	c := rtRouter(st, sprint, "route-c", prC, holdC)
	doneC := make(chan error, 1)
	go func() { doneC <- c.Run(ctxC) }()
	rtUntil(t, "route-c running after route-a stopped", func() bool {
		v, _ := client.HGet(context.Background(), LeaseKey(sprint), "instance").Result()
		return v == "route-c" && prC.passes.Load() > 0
	})

	// The lease taken away (a fenced instance): the holder stops every rule
	// and Run says why.
	must(t, client.HSet(context.Background(), LeaseKey(sprint), "instance", "route-x", "token", "x").Err())
	var errC error
	rtUntil(t, "route-c stopping on the lost lease", func() bool {
		select {
		case errC = <-doneC:
			return true
		default:
			return false
		}
	})
	if !errors.Is(errC, ErrLeaseLost) {
		t.Fatalf("route-c Run = %v; want the lease lost", errC)
	}
	stopped := prC.passes.Load()
	time.Sleep(50 * time.Millisecond)
	if prC.passes.Load() != stopped {
		t.Fatal("a rule kept passing after its router lost the lease")
	}
	if v, _ := client.HGet(context.Background(), LeaseKey(sprint), "instance").Result(); v != "route-x" {
		t.Fatalf("the fenced instance released a lease it no longer holds (holder %q)", v)
	}
}
